# sprout SSE Endpoint 设计

- 日期：2026-11-17
- 模块：`github.com/wxy365/sprout`
- 状态：待评审

## 1. 背景与目标

sprout 目前只支持「一次请求返回一次响应」的模式：端点处理函数返回一个输出结构体，框架把它序列化后写出。SSE（Server-Sent Events）需要的是**一条长连接上持续推送多个事件**，与现有模型不兼容。

目标：让 sprout 原生支持 SSE 端点，且不破坏现有的 `Endpoint[I,O]` / `Mount` 语义。

非目标（本期不做）：WebSocket、客户端 SDK、异步广播 hub（多连接共享同一数据源）。

## 2. 决策摘要

| 决策点 | 结论 | 理由 |
|---|---|---|
| API 形态 | 新增独立的 `SSEEndpoint[I]` + `MountSSE` | SSE 与普通 REST 端点的生命周期、错误语义都不同，混在一个类型里会让 `Endpoint` 语义变胖 |
| 推送模型 | 同步：handler 内循环发送 | 覆盖轮询式推流；异步广播 hub 复杂度高、收益不确定，留待后续 |
| 响应头提交 | 懒提交：第一帧写出时才发响应头 | 校验/限流/熔断失败仍能返回正确的 4xx/5xx JSON 状态码 |
| 心跳默认值 | 关闭，opt-in（`KeepAlive` 字段） | 不产生意外的后台 goroutine；跨代理场景由使用者显式开启 |
| `Connection` 响应头 | 仅 HTTP/1.1 下发 | RFC 9113 禁止 HTTP/2/3 携带连接专属头 |
| 数据序列化 | `string`/`[]byte` 原样，其余 JSON | 与框架其余部分统一用 `basal/json` |

## 3. 公共 API

新增文件 `sprout/sse.go`，并在 `sprout/api.go` 按该文件「公共 API 汇总」的既有约定补上别名与再导出。

### 3.1 类型

```go
// SSEEvent 表示一个服务端推送事件。
type SSEEvent struct {
	// ID 是事件 ID，写入 "id" 字段。空值不写该字段。
	ID string
	// Event 是事件名，写入 "event" 字段。空值不写该字段。
	Event string
	// Data 是事件载荷，写入 "data" 字段。
	// string 与 []byte 原样输出，其余类型经 basal/json 序列化。
	Data any
	// Retry 是客户端重连间隔，写入 "retry" 字段（毫秒）。非正值不写该字段。
	Retry time.Duration
}

// SSEHandler 是 SSE 端点的业务逻辑签名。
// 处理函数同步发送事件，返回即结束该连接。
type SSEHandler[I any] func(ctx *Context, in I, stream *SSEStream) error

// SSEEndpoint 定义一个 SSE 端点。
type SSEEndpoint[I any] struct {
	// Name 是端点名，用于日志与熔断器标识。
	Name string
	// Pattern 是 URI 路径模式，支持静态段、命名参数、格式占位符与通配符，
	// 与 Endpoint.Pattern 一致。
	Pattern string
	// Methods 是该端点响应的 HTTP 方法列表。
	Methods []string
	// Handler 是业务逻辑函数。
	Handler SSEHandler[I]
	// Interceptors 是该端点的拦截器列表。
	Interceptors []Interceptor
	// ErrorHandler 是该端点的错误处理器。为 nil 时回退到服务器级错误处理器。
	ErrorHandler ErrorHandler
	// KeepAlive 是心跳间隔。大于 0 时，框架周期性地向连接写入注释行
	// （": ping"），用于穿过会掐断空闲连接的中间代理。0 表示关闭。
	KeepAlive time.Duration
}
```

### 3.2 挂载

```go
// MountSSE 把一个 SSE 端点挂载到应用中的某个服务器上。
// 与 Mount 的行为一致：未指定服务器名时挂到 App.Servers 的第一个服务器。
func MountSSE[C any, I any](e *SSEEndpoint[I], a *App[C], svrName ...string)
```

`C`、`I` 均由参数推导，调用方无需显式写出类型参数。

### 3.3 流句柄

```go
// SSEStream 是向一个 SSE 响应写入事件的句柄。
type SSEStream struct { /* unexported */ }

func (s *SSEStream) Send(ev SSEEvent) error   // 发送一个事件
func (s *SSEStream) SendData(data any) error  // 只带 data 的简写
func (s *SSEStream) SendEvent(name string, data any) error // 具名事件
func (s *SSEStream) Comment(text string) error // 写一行注释（心跳）
func (s *SSEStream) Close() error               // 结束流
func (s *SSEStream) Done() <-chan struct{}     // 客户端断开或流结束时关闭
func (s *SSEStream) Err() error                 // 流的终止原因
```

语义要点：

- `Send` / `Comment` 在客户端已断开或流已关闭时返回错误（`errSSEClosed`）。
- `Close()` 在流尚未提交时会先提交响应头（200 + SSE 头）再结束连接，客户端因此拿到一个格式合法但为空的 SSE 响应。
- `Done()` 在**客户端断开**、**写失败**、**显式 Close()** 三种情况下都会关闭，handler 应当 `select` 它来退出循环。
- `Err()` 返回终止原因：客户端断开时是 `context.Canceled` 一类的请求上下文错误；写失败时是底层 I/O 错误；主动 `Close()` 时为 nil。
- 所有写操作由互斥锁串行化，心跳 goroutine 与 handler 可以并发调用。

## 4. 帧编码

按 WHATWG HTML Living Standard 的 Server-Sent Events 规范编码：

```
id: 42
event: tick
retry: 3000
data: {"n":1}

```

规则：

1. 字段顺序固定为 `id` → `event` → `retry` → `data`，空值字段不写。
2. `id` / `event` 的值剔除 `\r` 与 `\n`（规范禁止字段值含换行）。
3. `retry` 以毫秒为单位输出。
4. `data`：`string` 与 `[]byte` 原样输出；`nil` 输出 `null`；其余类型经 `basal/json` 序列化（紧凑格式）。
5. `data` 按 `\n` 拆行，每行单独加 `data: ` 前缀。
6. 每个事件以一个空行结束。

已知边界：`data` 为空字符串时，按浏览器规范该事件不会触发 `onmessage`（空 data 缓冲会被丢弃）。需要推送空载荷时传 `nil`（序列化为 `null`）或其它非空值。

## 5. 响应头

流提交时写出：

| 头 | 值 | 条件 |
|---|---|---|
| `Content-Type` | `text/event-stream` | 总是 |
| `Cache-Control` | `no-cache` | 总是 |
| `X-Accel-Buffering` | `no` | 总是（关闭 nginx 响应缓冲） |
| `Connection` | `keep-alive` | 仅 `ProtoMajor == 1` |

新增 MIME 常量 `MimeEventStream = "text/event-stream"`（`api.go` 再导出）。

## 6. 写超时

`startH2c` 硬编码了 `WriteTimeout: 30 * time.Second`（`sprout/server.go:86`），SSE 长连接会活不过 30 秒。

流创建时用 `http.NewResponseController(w).SetWriteDeadline(time.Time{})` 清除写超时。

已验证：

- `golang.org/x/net/http2` 的 `responseWriter` 与 quic-go v0.65.0 的 `http3.responseWriter` 都实现了 `SetWriteDeadline` 与 `Flush`，HTTP/1.1、h2c、HTTP/3 共用一套写法。
- 实测：服务端 `WriteTimeout` 设为 2s 时，清除 deadline 后连接持续 4.2s 正常 EOF。
- `httptest.ResponseRecorder` 不支持 `SetWriteDeadline`（返回 `http.ErrNotSupported`），实现必须容忍该错误；单元测试不依赖此行为。

## 7. 请求处理流程

```
mux.ServeHTTP
  └─ refinedEndpoint.httpHandler
       └─ 拦截器链 [recover, 熔断, 限流, CORS] + 用户拦截器
            └─ 解析入参（parseHttpRequest）
            └─ 参数校验（validateFunc）
            └─ 创建 SSEStream，登记到请求上下文
            └─ 调用 handler（内层 recover 兜底）
                 ├─ 返回 error：
                 │    ├─ 流已提交 → 发 event: error 帧 → 关闭流
                 │    └─ 流未提交 → 向上返回错误 → 走标准 JSON 错误路径
                 └─ 返回 nil → 关闭流
```

要点：

- **懒提交**：SSE 响应头在第一次写帧时才发出。因此入参校验失败、限流、熔断都仍然返回标准的 JSON 错误与正确的状态码，行为与普通端点一致。
- **未发送任何事件就正常返回**：关闭流时提交响应头，客户端得到一个格式合法但为空的 SSE 响应，随后连接关闭。
- **内层 recover**：handler 内的 panic 被就地转成 `errs.Err`（500），走上面的错误分支，**不会**把 JSON 写进 SSE 流。外层的内置 `recoverInterceptor` 保留，用于兜住拦截器自身的 panic。

## 8. 错误处理

SSE 端点的错误处理与普通端点保持同一优先级：**端点 ErrorHandler > 服务器 ErrorHandler > 默认处理器**，但增加一条「流已提交」的前置判断：

| 情形 | 行为 |
|---|---|
| 流未提交时出错 | 走标准错误处理器：写状态码 + JSON 错误体 |
| 流已提交后出错 | 写 `event: error` 帧，`data` 为 `{"code":...,"message":...,"status":...}`，随后关闭流。状态码已无法更改 |
| handler panic | 内层 recover 转成 500 错误，按上两行处理 |
| 写失败（客户端断开） | `Send` 返回 I/O 错误，流终止；不再尝试写 error 帧 |

`event: error` 的载荷取自 `errs.Err` 的 `Code` / `Message` / `Status` 字段（存在时才带上）。`Message` 为空时回退到 `err.Error()`；不是 `errs.Err` 的错误只带 `message`。

## 9. 断连与心跳

- **断连感知**：流创建时起一个 goroutine 等待请求上下文结束；客户端断开时请求上下文被取消，该 goroutine 关闭 `Done()` 并记录 `Err()`。handler 返回后请求上下文随之取消，goroutine 自然退出，无泄漏。
- **心跳**：`KeepAlive > 0` 时另起一个 goroutine，按间隔调用 `Comment("ping")` 写注释行 `: ping\n\n`。注释行对浏览器不可见，不会触发 `onmessage`，只用于维持连接。流结束时该 goroutine 退出。

## 10. 与现有框架的集成

- **mux 不用改**：SSE 端点编译成同样的 `refinedEndpoint`，路由匹配、路径参数、方法匹配全部复用。
- **拦截器链复用**：熔断、限流、CORS 以及用户自定义拦截器原样生效。
- **入参解析与校验复用**：`parseHttpRequest` + `svr.buildInputEntityValidateFuncs`。
- **错误上下文**：handler 返回错误时，与普通端点一样把错误写入 `ctxKeyEndpointError`，让熔断器能正确判定失败。
- **`mount` 去重**：`app.go` 里的 `mount` 与新增的 `mountSSE` 逻辑相同，抽出共用的 `mountEndpoint` 辅助函数，二者都调用它。`mount` 是非导出函数，重构不影响公共 API。

## 11. mimo 演示端点

新增 `mimo/iface/sse/sse.go`：

```
GET /sse/tick?interval=1&count=10
```

- `interval`：推送间隔（秒），默认 1，范围 [1, 60]
- `count`：推送条数，默认 10，范围 [1, 100]
- 每条事件形如 `{"seq":1,"ts":"2026-11-17T12:00:00+08:00"}`，事件名为 `tick`，事件 ID 为递增序号
- handler 在推送完毕后返回，连接关闭

`mimo/api_test.http` 补一条 curl 示例。

## 12. 测试计划

新增 `sprout/sse_test.go`：

| 用例 | 断言 |
|---|---|
| 帧编码 | 各字段单独/组合输出；`id`/`event` 含换行时被剔除；多行 `data` 拆行；`string` 原样；结构体 JSON 化；`nil` → `null` |
| 端到端 | 响应头 `Content-Type` / `Cache-Control` / `X-Accel-Buffering` 正确；帧内容与顺序正确 |
| 懒提交 | handler 未发送任何事件时，响应体为空但头已提交 |
| 提交前出错 | 状态码为错误自带的状态码，响应体是 JSON 错误 |
| 提交后出错 | 响应体含 `event: error` 帧，状态码仍是 200 |
| panic | handler panic 产生 `event: error` 帧，而不是 JSON 混入流中 |
| 断连 | 客户端断开后 `Done()` 关闭，`Err()` 非 nil |
| 心跳 | `KeepAlive` 生效时流中出现 `: ping` 注释行 |
| 并发 | 多个 goroutine 并发 `Send` 不产生交错帧（每帧完整） |
| 写超时 | 真实 `httptest.Server` + `WriteTimeout` 下，流存活超过超时时间 |

沿用 `endpoint_test.go` 的 `serveEndpoint` 风格，新增对应的 `serveSSE` 辅助函数。

## 13. 明确不做的事

- WebSocket 支持
- `sprout/cli` 的 SSE 客户端
- 异步广播 hub / 订阅者注册
- 服务端事件重放（`Last-Event-ID` 请求头处理）
- SSE 客户端的库

## 14. 风险与已知约束

1. **中间代理**：nginx 等代理默认有读超时（`proxy_read_timeout` 60s），会掐断空闲连接。需要开启 `KeepAlive` 或调整代理配置。`X-Accel-Buffering: no` 只解决缓冲，不解决超时。
2. **浏览器重连**：`EventSource` 断线后会自动重连，但本框架不处理 `Last-Event-ID`，重连后的事件 id 由服务端自行保证连续性。
3. **写超时清除失败**：若某个自定义 `ResponseWriter` 不支持 `SetWriteDeadline`，SSE 流会继承服务端的 `WriteTimeout` 而被提前切断。此时会记一条 warn 日志。
4. **HTTP/2 与 HTTP/3 下无 `Connection` 头**：这是规范要求，不影响长连接本身。
