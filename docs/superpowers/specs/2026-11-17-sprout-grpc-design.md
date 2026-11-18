# sprout gRPC 集成设计

- 日期：2026-11-17
- 模块：`github.com/wxy365/sprout`（新增子包 `sprout/rpc`）
- 状态：待评审
- 相关：[[2026-11-17-sse-endpoint-design]]（同一模块的并发改动，见 §13）

## 1. 背景与目标

sprout 目前是一个纯 HTTP 的 REST 微框架。目标是把 gRPC 纳入 sprout，成为一套「同一个应用既能提供 HTTP 接口，也能提供 gRPC 服务，同时还能作为 gRPC 客户端调用远端服务」的框架。

最关键的一条约束来自使用者：

> **不需要用户手动编写 proto，不需要用户手动生成代码。**

同时不能因此牺牲互操作性——线格式必须仍然是标准 protobuf，否则跨语言调用无从谈起。

目标清单：

1. 用户用 Go 结构体定义消息，用 Go 函数定义方法，框架负责全部 gRPC 胶水层。
2. 发出去的是标准 protobuf 线格式，任何用 protoc 生成的客户端（Java / Go / Python…）都能直接调用 sprout 提供的服务。
3. 客户端既能直连固定地址，也能通过服务注册中心（Nacos）发现服务提供者。
4. 复用 sprout 既有的能力：结构体标签校验、错误模型 `errs.Err`、熔断与限流配置、配置体系、拦截器模型。

非目标（本期不做）：

- 不做 protoc 插件，不做代码生成。
- 不做 gRPC-Gateway（HTTP ↔ gRPC 转码）。
- 不做服务端反射的动态客户端（调用方连本地消息类型都不写的那种）。
- 不做运行时 `.proto` 解析（用户仍然不写 `.proto`）。
- 不做跨服务事务、连接池高级特性。

## 2. 决策摘要

| 决策点 | 结论 | 理由 |
|---|---|---|
| 消息定义方式 | Go 结构体 + `pb:"字段号"` 标签 | 满足「不写 proto、不生成代码」，且能合成出真实 protobuf 描述符 |
| 描述符来源 | 运行时反射合成 `descriptorpb.FileDescriptorProto` | 无需 protoc；合成结果与 protoc 产物线格式一致（已实测） |
| 传输层 | 原生 `google.golang.org/grpc` | 标准实现，自带 HTTP/2、负载均衡、重连、TLS |
| 服务注册中心 | Nacos | 用户明确选定 |
| Nacos 接入方式 | 自研轻量 HTTP 客户端（零新依赖） | 官方 SDK 会引入 zap / prometheus / aliyun-sdk，与既有 `basal/log` 形成双日志体系 |
| 通信模型 | 一元 + 双向流式 | 用户明确选定；服务端流式由双向流机制覆盖 |
| 客户端形态 | 类型安全 `Doer` / `StreamDoer` | 与既有 `sprout/cli.Doer` 风格一致 |
| 代码位置 | sprout 模块内新增子包 `sprout/rpc` | 与既有 `sprout/cli` 同级；不 import 即不编译 grpc |
| 拦截器 | 复用既有熔断/限流配置与守卫逻辑 | 两套传输共享同一份 `app.breakers` / `app.limiters` 配置语义 |
| 入参校验 | 复用 `validate` 标签体系 | 消息本就是 Go 结构体，校验器可直接套用 |

## 3. 方案对比与选型

需要在「用户不写 proto」的前提下拿到标准线格式，有三条路：

| 方案 | 用户要做什么 | 线格式 | 结论 |
|---|---|---|---|
| **A. 运行时合成描述符** | 定义 Go 结构体 | 标准 protobuf | **采用**。已用 spike 实测验证 |
| B. 自定义 JSON codec 跑在 gRPC 传输上 | 定义 Go 结构体 | 自定义，非标准 | 跨语言不可用，等于放弃 gRPC 的主要价值 |
| C. 运行时编译 `.proto` | 仍要写 `.proto` | 标准 protobuf | 只省了 protoc 一步，不满足核心诉求 |

## 4. 已验证事实（spike）

在写设计之前，用 `/tmp/grpc-spike` 做了可行性验证，结论如下：

| 验证项 | 结果 |
|---|---|
| Go 结构体 → `FileDescriptorProto` → `protodesc.NewFile` | 通过 |
| 合成描述符 → `dynamicpb` 消息 → 原生 `grpc-go` 起服务 | 通过，真实 gRPC 服务可用 |
| 客户端 → 服务端 → 客户端完整回环 | 通过，嵌套结构 / map / repeated / bytes 全部正确 |
| 与 protoc 生成类型（`wrapperspb` / `timestamppb`）的线字节比对 | 逐字节一致 |
| gRPC status code 跨进程传递 | `InvalidArgument` 原样到达客户端 |
| `time.Time` → `google.protobuf.Timestamp`（引用 well-known type） | 通过 |

线字节比对是本设计的核心证据：它证明「不写 proto」不等于「自造私有格式」。

## 5. 包结构

新增子包 `sprout/rpc`（与 `sprout/cli` 同级，同一 Go module）：

```
sprout/rpc/
├── descriptor.go   结构体 → protobuf 描述符合成
├── convert.go      Go 结构体 ↔ dynamicpb 双向转换
├── service.go      Service / Method / Unary / Stream 定义
├── server.go       rpc.Server，与 sprout.App 的挂载与生命周期
├── stream.go       ServerStream[I,O] / ClientStream[I,O]
├── client.go       Client / Doer / StreamDoer
├── resolver.go     Registry 接口 + gRPC resolver.Builder 实现
├── nacos.go        Nacos 注册中心适配
├── errors.go       errs.Err ↔ gRPC status 双向映射
└── interceptor.go  gRPC 拦截器（panic 恢复 / 熔断 / 限流）
```

新增依赖：

```
google.golang.org/grpc        v1.85.0   （要求 go1.26，与 sprout 一致）
google.golang.org/protobuf    v1.36.12
```

`google.golang.org/genproto/googleapis/rpc` 是 grpc 的既有间接依赖，`errdetails.ErrorInfo` 来自该模块，不引入新依赖。

因为 grpc 依赖只出现在 `sprout/rpc` 子包，不 import 该子包的项目不会编译 grpc 代码。

## 6. 消息定义与描述符合成

### 6.1 标签

结构体字段用 `pb` 标签声明字段号：

```go
type HelloReq struct {
	Name string `pb:"1"`
	Age  int32  `pb:"2"`
}
```

- `pb` 标签缺失或字段号非法（非正整数）的字段，**在注册服务时报错**，不做静默忽略。理由：字段号错位会导致线格式不兼容且无法自检，静默忽略会把问题推迟到运行时。
- 字段号必须唯一；重复即报错。

### 6.2 类型映射

| Go 类型 | protobuf 类型 | 备注 |
|---|---|---|
| `string` | `string` | |
| `bool` | `bool` | |
| `int32` / `int64` | `int32` / `int64` | |
| `uint32` / `uint64` | `uint32` / `uint64` | |
| `float32` / `float64` | `float` / `double` | |
| `[]byte` | `bytes` | |
| `[]T`（T 为标量或结构体） | `repeated T` | |
| `map[K]V` | `map<K,V>` | 合成 `<字段名>Entry` 嵌套类型 |
| 结构体 / `*结构体` | 嵌套 message | |
| `*标量`（如 `*int32`） | proto3 optional | 显式存在性，用合成 oneof 表达 |
| `time.Time` | `google.protobuf.Timestamp` | 文件加 `google/protobuf/timestamp.proto` 依赖 |
| 具名整型（`type Status int32`） | `int32` | 线格式与 enum 一致；不做 enum 描述符 |

map 的键支持 `string` / `bool` / `int32` / `int64` / `uint32` / `uint64`；值支持标量、`[]byte` 与结构体。其它键类型在注册时报错。

### 6.3 命名规则

- proto 字段名 = Go 字段名（首字母大写）。
- json_name = Go 字段名的小驼峰形式（与 protoc 的默认 JSON 名规则一致）。
- 消息全名 = `<proto 包名>.<Go 结构体名>`。
- proto 包名从 `Service.Name` 推导：`"demo.Greeter"` → 包名 `demo`，服务名 `Greeter`。若 `Service.Name` 不含点号，包名默认取 `sprout`。

### 6.4 嵌套结构体的处理

嵌套结构体**提升为顶层 message**，而不是 protobuf 的嵌套 message：

```go
type Address struct { City string `pb:"1"` }
type HelloReq struct { Addr *Address `pb:"2" }   // → message demo.Address
```

理由：提升后描述符构建不需要处理递归嵌套命名，跨服务共享同一消息类型也不会产生命名冲突。

**线格式不受影响**——protobuf 线格式只取决于字段号与字段类型，与消息的类型名无关。类型名只影响 JSON 表示与服务端反射的输出，跨语言调用不受影响。

`map` 的 entry 类型是 protobuf 的硬性要求，必须嵌套在所属消息内，命名 `<字段名>Entry`。

### 6.5 文件与服务描述符

每个 `Service` 生成一个 protobuf 文件：

- 文件名：`<包名>/<服务名小写>.proto`，例如 `demo/greeter.proto`。
- 文件内含该服务用到的全部消息（含被提升的嵌套类型）。
- `grpc.ServiceDesc` 的 `ServiceName` = `Service.Name`，`Metadata` = 文件名。
- `HandlerType` 用 `(*any)(nil)`：动态服务的实现类型是框架内部的包装类型，没有公共接口可供声明，而 grpc-go 只用它做一次 `Implements` 检查与反射元数据，服务名取自 `ServiceDesc.ServiceName`，不受影响。

文件描述符会尝试注册进 `protoregistry.GlobalFiles`，从而让 grpc-go 的标准服务端反射可用（`grpcurl` 等工具可自省本服务）。注册冲突时记录一条 warn 并跳过，不影响服务本身工作。

## 7. 服务端

### 7.1 公共 API

```go
// Service 描述一个 gRPC 服务及其方法。
type Service struct {
    // Name 是服务的全限定名，例如 "demo.Greeter"。
    Name string
    // Methods 是该服务的方法列表。
    Methods []Method
}

// Method 描述一个 gRPC 方法。由 Unary / Stream 构造，不直接实例化。
type Method interface { /* 未导出方法 */ }

// Unary 构造一元方法。I 为请求类型，O 为响应类型。
func Unary[I, O any](name string, handler func(ctx *Context, in *I) (*O, error)) Method

// Stream 构造双向流方法。
func Stream[I, O any](name string, handler func(ctx *Context, stream *ServerStream[I, O]) error) Method
```

类型参数由 handler 签名推导，调用方不需要显式写出：

```go
svc := &rpc.Service{
    Name: "demo.Greeter",
    Methods: []rpc.Method{
        rpc.Unary("Hello", func(ctx *rpc.Context, in *HelloReq) (*HelloResp, error) {
            return &HelloResp{Greeting: "hello " + in.Name}, nil
        }),
        rpc.Stream("Chat", func(ctx *rpc.Context, s *rpc.ServerStream[ChatReq, ChatResp]) error {
            for {
                in, err := s.Recv()
                if err == io.EOF {
                    return nil
                }
                if err != nil {
                    return err
                }
                if err := s.Send(&ChatResp{Text: in.Text}); err != nil {
                    return err
                }
            }
        }),
    },
}
```

`Unary` / `Stream` 是包级泛型函数而非方法——Go 不允许在非泛型类型上定义泛型方法，这样写才能让类型参数自动推导。

### 7.2 挂载

```go
// Mount 把一个 gRPC 服务挂载到应用中指定的 gRPC 服务端上。
// 与 sprout 的 Mount 语义一致：未指定服务端名时挂到第一个 gRPC 服务端。
func Mount[C any](svc *Service, a *sprout.App[C], svrName ...string)
```

挂载通过 `App.Initializers` 完成，保证在服务端启动之前完成注册。目标服务端从 `App.Runners` 中按名字查找 `*rpc.Server`。

### 7.3 服务端实例

```go
type Server struct {
    Name            string
    Port            uint16
    CertFile        string
    KeyFile         string
    Debug           bool
    ShutdownTimeout time.Duration
}

// NewServer 创建一个 gRPC 服务端实例，并从 app.grpc_servers 配置中补全同名项。
func NewServer(name string) *Server

// ServersFromConfig 按 app.grpc_servers 配置创建全部 gRPC 服务端实例。
func ServersFromConfig() []*Server
```

配置节与既有 `app.servers` 对齐：

```json
{
  "app": {
    "grpc_servers": [
      { "name": "grpc", "port": 9090, "cert_file": "", "key_file": "", "debug": false, "shutdown_timeout": 10000 }
    ]
  }
}
```

`Server` 实现 sprout 的 `Runner` 接口（见 §12.1），随 `App.Run()` 一起启动。收到 `SIGINT` / `SIGTERM` 时先 `GracefulStop()`，超过 `ShutdownTimeout` 则强制 `Stop()`。

### 7.4 请求处理流程

```
gRPC 传输层
  → 标准 proto codec（dynamicpb 承载）
  → 服务端拦截器链 [panic 恢复, 熔断, 限流] + 用户拦截器
  → 按方法名路由到 handler
  → dynamicpb 消息 → 转为 Go 结构体
  → 结构体校验（复用 validate 标签体系）
  → 调用用户 handler
  → Go 结构体 → dynamicpb 消息 → 返回
```

### 7.5 入参校验复用

消息是 Go 结构体，因此 sprout 既有的 `validate` 标签体系（`required` / `email` / `required_by` / `either` / 区间）可以直接套用，无需为 gRPC 另写一套。

实现上把 `(*httpServer).buildInputEntityValidateFuncs` 从方法抽成包级函数 `buildInputEntityValidateFuncs(validators []validator, inputType reflect.Type) objectValidateFunc`，HTTP 侧保留原方法作为薄封装，行为不变。

校验失败返回 `errs.Err`（状态码 400），经 §10 的映射成为 gRPC `InvalidArgument`。

## 8. 流式

### 8.1 服务端流

```go
type ServerStream[I, O any] struct { /* 未导出字段 */ }

func (s *ServerStream[I, O]) Recv() (*I, error)      // io.EOF 表示客户端已半关闭
func (s *ServerStream[I, O]) Send(out *O) error
func (s *ServerStream[I, O]) Context() context.Context
func (s *ServerStream[I, O]) SetHeader(md metadata.MD) error
func (s *ServerStream[I, O]) SendHeader(md metadata.MD) error
func (s *ServerStream[I, O]) SetTrailer(md metadata.MD)
```

- `Recv` 在客户端半关闭后返回 `io.EOF`，这是 grpc-go 的约定，handler 应据此退出循环。
- `Send` 在流已结束或客户端断开时返回错误。
- 每次 `Recv` / `Send` 都做一次 dynamicpb ↔ Go 结构体转换。

### 8.2 客户端流

```go
type ClientStream[I, O any] struct { /* 未导出字段 */ }

func (s *ClientStream[I, O]) Send(in *I) error
func (s *ClientStream[I, O]) Recv() (*O, error)      // io.EOF 表示流正常结束
func (s *ClientStream[I, O]) CloseSend() error
func (s *ClientStream[I, O]) Context() context.Context
func (s *ClientStream[I, O]) Header() (metadata.MD, error)
func (s *ClientStream[I, O]) Trailer() metadata.MD
```

### 8.3 服务端流式的兼容

「只回不收」的服务端流式方法用同一套双向流机制实现：客户端 `Send` 一次后 `CloseSend()`，服务端 `Recv` 得到 `io.EOF` 后继续把剩余结果发完。线格式互通，无需单独实现。

### 8.4 流的生命周期与并发

- 每个流对应一条 HTTP/2 流，服务端为其分配独立 goroutine。
- `ServerStream` 的 `Recv` / `Send` 由 grpc-go 串行化：同一流上 `Recv` 与 `Send` 可并发调用（不同方向），但**不应并发调用两个 `Recv` 或两个 `Send`**。这一点在文档中明确，使用者若需并发收取应自行加锁。
- `ServerStream` 在流结束后不可复用；`Send` 之后服务端返回即结束流。

## 9. 客户端与寻址

### 9.1 公共 API

```go
type Client struct { /* 未导出字段 */ }

// NewClient 创建一个针对指定 gRPC 服务的客户端。
func NewClient(service string, opts ...ClientOption) (*Client, error)

// Doer 返回一个类型安全的一元调用闭包，与 sprout/cli.Doer 风格一致。
func Doer[I, O any](c *Client, method string) func(ctx context.Context, in *I) (*O, error)

// StreamDoer 返回一个类型安全的双向流调用闭包。
func StreamDoer[I, O any](c *Client, method string) func(ctx context.Context) (*ClientStream[I, O], error)
```

```go
client, err := rpc.NewClient("demo.Greeter",
    rpc.WithNacos(rpc.NacosConfig{
        Servers: []string{"127.0.0.1:8848"},
        Group:   "DEFAULT_GROUP",
    }),
    rpc.WithBalancer("round_robin"),
    rpc.WithTimeout(3*time.Second),
)

hello := rpc.Doer[HelloReq, HelloResp](client, "Hello")
resp, err := hello(ctx, &HelloReq{Name: "wxy"})

chat := rpc.StreamDoer[ChatReq, ChatResp](client, "Chat")
stream, err := chat(ctx)
```

### 9.2 客户端选项

| 选项 | 作用 |
|---|---|
| `WithDirect(addr string)` | 直连 `host:port`，目标为 `passthrough:///host:port` |
| `WithNacos(cfg NacosConfig)` | 通过 Nacos 发现服务提供者 |
| `WithRegistry(r Registry)` | 自定义注册中心（可插拔） |
| `WithBalancer(name string)` | 负载均衡策略，默认 `round_robin` |
| `WithTLS(cfg *tls.Config)` | TLS 配置 |
| `WithInsecure()` | 明文连接（默认行为，与 grpc-go 一致） |
| `WithTimeout(d time.Duration)` | 单次调用超时 |
| `WithMetadata(md metadata.MD)` | 每次调用附加的元数据 |
| `WithUnaryInterceptor(i grpc.UnaryClientInterceptor)` | 一元调用拦截器 |
| `WithStreamInterceptor(i grpc.StreamClientInterceptor)` | 流式调用拦截器 |

`WithDirect` 与 `WithNacos` / `WithRegistry` 互斥，同时指定时报错。

### 9.3 寻址与 resolver

- 直连：目标串 `passthrough:///host:port`，不经过自定义 resolver。
- 注册中心：目标串 `sprout:///<gRPC 服务名>`，查询参数携带注册中心侧的服务名与分组，例如
  `sprout:///demo.Greeter?service=demo-greeter&group=DEFAULT_GROUP&namespace=prod`。
- 自定义 resolver 通过 `grpc.WithResolvers(...)` **按客户端注入**，不做全局 scheme 注册，因此多个客户端可以使用不同的注册中心实现而互不干扰。
- resolver 负责把实例列表推给 gRPC，由 grpc-go 原生负责重连与负载均衡。

### 9.4 Registry 接口

```go
// Registry 把服务名解析为服务提供者实例列表。
type Registry interface {
    // Discover 订阅指定服务的实例列表，返回实例更新的通道。
    // 通道关闭表示订阅结束。
    Discover(ctx context.Context, service string) (<-chan []Instance, error)
}

// Instance 是一个服务提供者实例。
type Instance struct {
    Addr     string            // host:port
    Healthy  bool
    Weight   float64
    Metadata map[string]string
}
```

## 10. 错误映射

`errs.Err` 与 gRPC status 双向映射，使 HTTP 与 gRPC 两条链路对调用方同构。

| HTTP 状态 | gRPC code |
|---|---|
| 400 | `InvalidArgument` |
| 401 | `Unauthenticated` |
| 403 | `PermissionDenied` |
| 404 | `NotFound` |
| 409 | `Aborted` |
| 429 | `ResourceExhausted` |
| 499 | `Canceled` |
| 500 | `Internal` |
| 503 | `Unavailable` |
| 504 | `DeadlineExceeded` |

- **服务端**：handler 返回 `*errs.Err` 时，按 `Status` 映射出 gRPC code，`Message` 作为 status message，`Code` 字符串与 `Cause` 通过 `errdetails.ErrorInfo` 放入 status details。非 `errs.Err` 的错误统一映射为 `Internal`。
- **客户端**：gRPC 错误还原为 `*errs.Err`，`Status` 为等价的 HTTP 状态码，`Code` 取自 `ErrorInfo`（无则取 gRPC code 名），`Message` 为 status message。调用方可用 `errors.As` 取出，与 HTTP 侧写法一致。

## 11. 拦截器

### 11.1 gRPC 拦截器形态

```go
// UnaryInterceptor 拦截一元调用。
type UnaryInterceptor = grpc.UnaryServerInterceptor

// StreamInterceptor 拦截流式调用。
type StreamInterceptor = grpc.StreamServerInterceptor
```

直接采用 grpc-go 的形态，不另造一套——用户若已熟悉 grpc-go，无需重新学习。

### 11.2 内置拦截器

服务端默认链：**[panic 恢复, 熔断, 限流] + 用户拦截器**。

- **panic 恢复**：捕获 handler panic，转成 `Internal` 错误，原始 panic 与栈记入日志。
- **熔断**：复用既有 `app.breakers` 配置，键为方法名（如 `demo.Greeter/Hello`），沿用 gobreaker 的两段式断路器语义。
- **限流**：复用既有 `app.limiters` 配置，键为方法名，服务端维度限流照旧；客户端维度在 gRPC 下没有源 IP，改为读取元数据中的 `client-id`，缺失时不做客户端维度限流。

### 11.3 既有拦截器逻辑的抽取

`sprout/interceptor.go` 中的熔断器与限流器目前内嵌在 HTTP 拦截器里。本次把二者抽成可复用的守卫对象：

```go
// circuitBreakerGuard 封装熔断器的放行与结果上报。
type circuitBreakerGuard struct{ /* ... */ }

// rateLimiterGuard 封装令牌桶的放行判断。
type rateLimiterGuard struct{ /* ... */ }
```

HTTP 拦截器与 gRPC 拦截器共用同一份守卫实现与同一份配置读取逻辑（`getCircuitBreakerSettings` / `getRateLimiterSettings`）。HTTP 侧行为不变，由既有单测保证。

## 12. 对既有 sprout 的改动

共 4 处，都是小改动，不改变既有公共 API 的语义。

### 12.1 `app.go`：新增 Runner 生命周期

核心包不能 import `sprout/rpc`（会形成循环依赖），因此用接口解耦：

```go
// Runner 是随应用一起启动与停止的组件。
type Runner interface {
    // Start 阻塞运行，直到组件停止。
    Start() error
    // Stop 优雅停止组件。
    Stop(ctx context.Context) error
}
```

`app` 结构体新增 `Runners []Runner` 字段。`App.Run()` 的启动顺序调整为：

1. 执行 `init()`（含 `Initializers`，因此 gRPC 服务已挂载完成）。
2. 启动全部 Runner（HTTP 服务 + gRPC 服务），除第一个外都在后台 goroutine 中运行。
3. 第一个组件在前台阻塞，保持既有「第一个 HTTP 服务阻塞、其余后台」的语义不变。

当应用没有 HTTP 服务、只有 gRPC 服务时，第一个 Runner 阻塞，进程不会提前退出。

### 12.2 `server.go`：抽出并导出校验函数

`buildInputEntityValidateFuncs` 位于 `server.go`（不是 `validator.go`），当前是 `(*httpServer)` 的方法。`sprout/rpc` 是**另一个包**，无法访问核心包的未导出标识符，因此除了抽取为包级函数，还必须经 `api.go` 导出：

```go
// api.go
// DefaultValidators 返回框架内置的校验器列表。
func DefaultValidators() []Validator

// BuildInputEntityValidateFuncs 按给定的校验器列表构建入参校验函数。
func BuildInputEntityValidateFuncs(validators []Validator, inputType reflect.Type) ObjectValidateFunc
```

核心包内部把方法体抽成包级 `buildInputEntityValidateFuncs(validators []validator, inputType reflect.Type) objectValidateFunc`，`(*httpServer).buildInputEntityValidateFuncs` 保留为薄封装，既有调用点不变。

`Validator` / `ValidateFunc` / `ObjectValidateFunc` 在 `api.go` 中已有类型别名，跨包可直接使用——`validator` 接口的方法 `ValidateFunc(...)` 返回的就是别名的 `ValidateFunc`，因此外部包既能持有 `[]Validator` 也能调用它。

### 12.3 `interceptor.go`：抽出并导出熔断与限流守卫

见 §11.3。HTTP 行为不变。

同样因为跨包复用，守卫类型必须经 `api.go` 导出：

```go
// api.go
type CircuitBreakerGuard = circuitBreakerGuard
type RateLimiterGuard = rateLimiterGuard

// NewCircuitBreakerGuard 按方法名创建熔断守卫，配置取自 app.breakers。
func NewCircuitBreakerGuard(name string) *CircuitBreakerGuard

// NewRateLimiterGuard 按方法名创建限流守卫，配置取自 app.limiters。
func NewRateLimiterGuard(name string) *RateLimiterGuard
```

守卫的接口形状要能同时服务 HTTP 与 gRPC：限流守卫的客户端维度在 HTTP 下取源 IP，在 gRPC 下取元数据里的 `client-id`，因此接口按**字符串标识**设计，而不是按 `*http.Request`：

```go
// Allow 放行一次请求。clientIdentity 为空表示不做客户端维度限流。
func (g *RateLimiterGuard) Allow(clientIdentity string) bool
```

HTTP 拦截器负责把 `*http.Request` 换算成标识再传入，gRPC 拦截器传入元数据中的标识。

### 12.4 `api.go`：补齐公共 API

按 `api.go` 既有约定（类型别名 + 薄再导出）补充 `Runner`、`Service`、`Method`、`Server`、`Client`、`Context`、`ServerStream`、`ClientStream`、`Registry`、`Instance` 等类型别名，以及 `Mount`、`NewClient`、`NewServer`、`ServersFromConfig`、`Doer`、`StreamDoer` 等再导出函数。

注意：`sprout/rpc` 是独立包，`api.go` 汇总的是 `sprout` 包自身的 API，因此 `rpc` 包的公共 API 在 `sprout/rpc` 自身文件内直接导出即可，不需要在 `api.go` 重复汇总。`api.go` 只需补充 `Runner` 接口。

### 12.5 文档

- `CLAUDE.md` 增加 `sprout/rpc` 子包的说明，并更新模块依赖图（新增 grpc / protobuf 依赖）。
- `mimo/api_test.http` 补充 gRPC 调用示例（grpcurl 或 Go 示例）。

## 13. 与并发 SSE 改动的协调

同目录下另一个会话正在实现 SSE 端点（`2026-11-17-sse-endpoint-design.md`），双方都要改 `sprout/app.go` 与 `sprout/api.go`：

| 文件 | SSE 侧改动 | gRPC 侧改动 | 冲突程度 |
|---|---|---|---|
| `app.go` | 抽出 `mountEndpoint`，SSE 走同一挂载路径 | 新增 `Runner` 接口与 `Runners` 字段 | 同文件、不同函数，可叠加 |
| `api.go` | 补 `SSEEndpoint` / `SSEStream` 等别名 | 补 `Runner` 别名 | 同文件、不同段落，可叠加 |
| `validator.go` | 复用 `svr.buildInputEntityValidateFuncs` | 把该方法抽成包级函数 | 同一函数，SSE 侧需同步 |
| `server.go` | 无 | 抽出并导出校验函数（§12.2） | 无冲突 |
| `interceptor.go` | 无 | 抽出并导出熔断/限流守卫（§12.3） | 无冲突 |
| `endpoint.go` | 修复 `ctx` 自引用缺陷（§13.2） | 不改 | 无冲突 |
| `parse_request.go` | 不改（绑定顺序是既定契约，§13.3） | 不改 | 无冲突 |

处理原则：两侧改动都保持「只增不减」的增量形态；`validator.go` 的抽取由 gRPC 侧完成，SSE 侧在其之后落地即可（抽取后 SSE 的调用点从 `svr.buildInputEntityValidateFuncs(...)` 变为 `buildInputEntityValidateFuncs(svr.Validators, ...)`，是机械替换）。

两个会话都不使用 `git add .`，各自只暂存自己文件的改动。

### 13.1 已确认的落地顺序

双方约定：**SSE 侧先落地，gRPC 侧在 SSE 的工作树版本上叠加增量**。gRPC 侧在 SSE 改动进入工作树之前，不修改 `app.go` / `api.go` / `validator.go`。

`validator.go` 的抽取由 gRPC 侧执行；SSE 侧在 `sse.go` 中只有一个调用点（`svr.buildInputEntityValidateFuncs(inputType)`），抽成包级函数后为机械替换，SSE 侧无需为此等待。

### 13.2 SSE 侧发现的 HTTP 路径问题

SSE 会话在实现过程中定位到 sprout HTTP 路径上的一处真实缺陷，由 SSE 侧修复：

**`endpoint.go` 的上下文自引用**：`context.WithValue(ctx, ctxKeyEndpointError, err)` 的父上下文是 `ctx`（`*requestContext`）本身，而 `requestContext.Value` 又转发到 `ctx.Request.Context()`，于是查询任何「不是刚设置的那个 key」时陷入自引用死循环。

gRPC 侧已独立复现确认（最小复现见下）：

```
RESULT: 查询该 key = boom          // 刚设置的 key 正常返回
RESULT: 查询非该 key 永挂 —— 自引用死循环复现
```

现有代码没暴露，是因为出错后唯一的查询恰好就是刚设的那个 key（熔断器 defer 读 `ctxKeyEndpointError`）。SSE 要往上下文里放「流」和「错误」两个值，用现有写法会直接挂住服务器。改法是父上下文换成 `ctx.Request.Context()`，保持 `*ctx.Request = *newRequest` 的原地修改风格不变。

**对 gRPC 方案的影响：无。** 该缺陷在 HTTP 请求处理路径上，而 gRPC 侧不经过 `requestContext`——gRPC 拦截器与 handler 使用 grpc-go 原生的 `context.Context`，消息编解码走 `dynamicpb`，与服务端的 `WriteTimeout` 无关（gRPC 有独立的 server 与 listener）。因此 gRPC 侧既不依赖这处修复，也不需要在自己的路径上重复处理它。

唯一需要在实现时留意的是：gRPC 侧的拦截器若要在 handler 返回后读取上下文值，必须使用 gRPC 原生 context 的 `context.WithValue` 语义，不要把 HTTP 侧的 `ctx.Request` 原地替换写法照搬到 gRPC 路径。

### 13.3 一个不是缺陷的既定契约

SSE 会话最初把 `parse_request.go` 中「`default` 标签的值会盖掉 query 参数实参」也报为缺陷，随后自查更正。gRPC 侧核查确认**该行为是被测试锁定的既定契约，不是缺陷**：

`sprout/parse_request_test.go` 的 `TestParseHttpRequestDefault`（第 109–114 行）以注释写明当前绑定顺序为 `path > default > query > header > cookie`，并断言同时带 `default` 与 `query` 标签的字段取的是默认值。

因此：

- 该行为**不得**按缺陷修复。改动它会改变框架公开的绑定语义，并打破既有测试 `TestParseHttpRequestDefault`。
- SSE 侧的 mimo 演示端点改用 `0` 作为「未指定」哨兵（校验区间放宽为 `[0,60]`，在 handler 内把 `0` 映射为 `1`）来规避绑定顺序，不依赖 `default` 标签。

**对 gRPC 方案的影响：无。** gRPC 路径没有请求参数绑定环节，消息由调用方直接构造，不存在「默认值与实参谁优先」的问题。gRPC 消息结构体上的 `validate` 标签只用于入参校验（§7.5），不参与取值。

## 14. 测试计划

### 14.1 描述符合成（`descriptor_test.go`）

- 各 Go 类型 → protobuf 类型的映射正确。
- 字段号、字段名、json_name 正确。
- 嵌套结构体提升为顶层 message。
- map 合成 `<字段名>Entry` 嵌套类型，键值类型正确。
- `*int32` 生成 proto3 optional（合成 oneof 名称为 `_<字段名>`）。
- `time.Time` 生成对 `google.protobuf.Timestamp` 的引用，且文件带 `google/protobuf/timestamp.proto` 依赖。
- 字段号缺失 / 非法 / 重复时报错。
- **线字节一致性**：合成的描述符解析 protoc 生成的字节后重新序列化，结果与原始字节逐字节一致。用 `wrapperspb`（string / bytes / int32 / int64 / bool / double）与 `timestamppb` 做对照。spike 已验证可行，直接固化为单测。

### 14.2 服务端（`server_test.go`）

- 一元调用成功，返回值正确。
- 入参校验失败返回 `InvalidArgument`。
- handler 返回 `errs.Err` 时 code / message / details 正确。
- handler panic 被恢复为 `Internal`，进程不崩。
- 熔断器打开后拒绝调用。
- 限流器生效。
- 多个方法、多个服务注册在同一服务端。
- 服务端优雅停止。

### 14.3 流式（`stream_test.go`）

- 双向流多轮收发，顺序正确。
- 客户端 `CloseSend` 后服务端 `Recv` 返回 `io.EOF`。
- 服务端返回后客户端 `Recv` 返回 `io.EOF`。
- 客户端中途断开，服务端 `Send` 返回错误且不 panic。
- 同一流上 `Send` / `Recv` 交叉并发不产生数据错乱。
- 多个并发流互不干扰。

### 14.4 客户端（`client_test.go`）

- 直连调用成功。
- 客户端错误还原为 `*errs.Err`，`errors.As` 可用。
- 超时生效。
- 元数据正确送达服务端。
- 客户端拦截器生效。

### 14.5 Nacos 与 resolver（`nacos_test.go`、`resolver_test.go`）

- 用 `httptest` 模拟 Nacos 开放 API：登录、实例列表。
- 实例列表变化后 resolver 推送新地址。
- 实例全部下线时推空列表，客户端进入连接失败状态而非崩溃。
- `round_robin` 把请求分摊到多个实例。
- Nacos 登录失败 / 接口异常时错误可读。

### 14.6 回归

- `basal` 与 `sprout` 既有全部测试保持通过。
- `interceptor.go` 与 `validator.go` 的抽取不改变 HTTP 行为（既有单测即为回归保证）。

## 15. mimo 演示

新增 `mimo/iface/rpc/demo.go`，提供一个演示服务：

- 服务名 `demo.Greeter`
- 一元方法 `Hello`：`HelloReq{Name, Age}` → `HelloResp{Greeting}`
- 双向流方法 `Chat`：客户端连续发 `ChatReq{Text}`，服务端逐条回 `ChatResp{Text}`

mimo 配置补 `app.grpc_servers` 一节（端口 9090），并在 `mimo/api_test.http` 补调用示例。

## 16. 风险与已知约束

1. **字段号必须与对端一致**：框架无法自检字段号是否与对端 `.proto` 对齐，错位会导致静默的语义错误。这是「不写 proto」换来的代价，靠约定与评审控制。
2. **消息类型名只影响 JSON 与反射**：嵌套结构体被提升为顶层 message，因此与对端 `.proto` 的消息全名可能不同；线格式与调用不受影响。
3. **`dynamicpb` 的性能开销**：每次调用都要做反射转换，相比 protoc 生成的静态代码有额外开销。高 QPS 场景需要压测确认可接受性；必要时可对同一类型缓存转换元数据（描述符已缓存，转换本身按字段逐个反射）。
4. **Nacos 开放 API 的版本差异**：Nacos 1.x 与 2.x 都保留 `/nacos/v1/*` 路径，本期以 v1 为准；若目标环境关闭了 v1 兼容层，需要改为 v2 路径。
5. **Nacos 鉴权**：`accessToken` 有 TTL，需要在过期前重新登录。
6. **双向流是最容易出并发问题的一块**，测试需重点覆盖并发 `Send`/`Recv` 与客户端断开。
7. **gRPC 依赖进入 sprout 的依赖图**：只影响 import 了 `sprout/rpc` 的项目；sprout 核心包的既有用户不会编译 grpc 代码，但 `go mod` 图中会出现这些依赖。

## 17. 实施顺序建议

1. 描述符合成 + 结构体 ↔ dynamicpb 转换 + 线字节单测（地基，风险最高的部分先落地）
2. `errs.Err` ↔ gRPC status 映射
3. 一元服务端 + 校验复用 + 拦截器复用 + `Runner` 生命周期接入
4. 一元客户端（直连）+ `Doer`
5. 双向流式（服务端与客户端）
6. Nacos 适配 + resolver + 负载均衡
7. mimo 演示 + `CLAUDE.md` 更新
