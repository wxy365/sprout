# sprout SSE Endpoint 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 sprout 框架原生支持 SSE（Server-Sent Events）端点，调用方可以用一个独立的 `SSEEndpoint[I]` 声明一个长连接推送端点，并通过 `MountSSE` 挂载。

**Architecture:** SSE 端点编译成与普通端点完全相同的 `refinedEndpoint`，因此 mux 路由、拦截器链、入参解析与参数校验全部复用，mux 本身零改动。区别只在于响应体怎么写：新增一个 `sseStream` 句柄，把 SSE 帧按 WHATWG 规范编码后写入响应并 flush。响应头**懒提交**（第一帧写出时才提交），这样入参校验失败、限流、熔断仍然返回标准的 JSON 错误与正确的 HTTP 状态码；流已提交后的错误则降级为 `event: error` 帧。

**Tech Stack:** Go 1.26、net/http、`http.ResponseController`（清写超时 + flush）、`basal/json`（事件载荷序列化）、`basal/errs`（结构化错误）。

**设计依据：** `sprout/docs/superpowers/specs/2026-11-17-sse-endpoint-design.md`

## Global Constraints

- Go 工具链：本机 `GOROOT` 指向 go1.25.0 SDK，而 PATH 里靠前的 `go` 是 Homebrew Go 1.27.3，两者不可混用。所有 go 命令用 `env -u GOROOT go ...`（Go 1.27 路径）。
- 构建命令必须在模块目录内执行，工作区根目录不是模块：`cd sprout && env -u GOROOT go build ./...`。
- 提交纪律：并发会话 `mvs_08af621e60a54ea4a2e2b214a0d8be68` 正在同一工作树做 gRPC 集成。**严禁 `git add .`**，每次只暂存自己改的文件。
- `docs/` 目录整体是 untracked，里面有对方的 `2026-11-17-sprout-grpc-design.md`。只 add `docs/superpowers/specs/2026-11-17-sse-endpoint-design.md` 和 `docs/superpowers/plans/2026-11-17-sse-endpoint.md`，不要 add 整个目录。
- 对方即将把 `(*httpServer).buildInputEntityValidateFuncs` 抽成包级函数 `buildInputEntityValidateFuncs(validators []validator, inputType reflect.Type) objectValidateFunc`。本计划调用的是**现有的方法形式** `svr.buildInputEntityValidateFuncs(inputType)`，对方抽取后是机械替换。
- 命名约束：核心包内**不要**使用 `Runner`、`Component`、`Lifecycle` 这些标识符（对方要占用 `Runner`）。本计划新增的名字只有 `SSEEvent`、`SSEStream`、`SSEHandler`、`SSEEndpoint`、`MountSSE`、`MimeEventStream`，内部类型 `sseEvent`、`sseStream`、`sseHandlerFunc`、`sseEndpoint`。
- 不修改 `sprout/parse_request.go`：`default` 标签优先于 query/header/cookie 是被 `parse_request_test.go` 明确锁定的既定绑定顺序（path > default > query > header > cookie），不是缺陷。
- **请求上下文的架构在 `445999e` 之后变了**：框架自带的值（serializer、acceptType、deserializer、contentTypeParams、pathParams、endpointErr）不再走 `context.WithValue` 链，而是装在 `requestContext.values`（`ctxValues` 结构体）里；`ctx.Value()` 先查 `values.lookup()`，查不到再回落到 `values.parent`（请求原本携带的上下文）。因此：
  - SSE 的流放进 `ctxValues` 的字段，**不新增上下文键**。
  - 端点错误用既有的 `ctx.setEndpointError(err)` 记录，不再自己拼 `context.WithValue`。
  - 旧写法 `context.WithValue(ctx, ...)` 会让 `ctx.Value()` 自引用死循环（`ctx.Request.Context()` 变回指向自身）。`445999e` 已经消除了这个写法，Task 1 改为锁定这一行为的回归护栏。
- 注释与文档一律用英文，与 sprout 现有代码风格一致；本计划中的中文仅用于面向用户的说明。

---

### Task 1: 锁定请求上下文的取值行为（回归护栏）

**Files:**
- Create: `sprout/context_test.go`（新建）
- 不修改 `sprout/endpoint.go`、`sprout/context.go`

**Interfaces:**
- Consumes: 无（纯护栏）
- Produces: `sprout/context_test.go` 里的 `TestContextLookupAfterEndpointError`。它守住一条后来者容易踩的坑：端点返回错误之后，再去读**别的**上下文值不会死循环。

**背景：**

`445999e` 之前，`sprout/endpoint.go` 这样记录端点错误：

```go
newRequest := ctx.Request.WithContext(context.WithValue(ctx, ctxKeyEndpointError, err))
*ctx.Request = *newRequest
```

`context.WithValue` 的父是 `ctx`（`*requestContext`）本身，而 `requestContext.Value` 又转发到 `ctx.Request.Context()`：

```go
func (c *requestContext) Value(key any) any {
	return c.Request.Context().Value(key)
}
```

赋值之后 `ctx.Request.Context()` 就是那个 `WithValue` 节点，于是查任何**不是** `ctxKeyEndpointError` 的 key 时，链路变成 `ctx.Value(k)` → `ctx.Request.Context().Value(k)` → `WithValue` 节点 → 父节点 `ctx` → `ctx.Value(k)` → 无限自引用。

已实测复现（查另一个 key → 2 秒超时不死）。

`445999e` 把框架的值收进了 `ctxValues` 结构体，`setEndpointError` 直接写字段，`ctx.Value()` 查不到时回落到 `values.parent`（请求原本的上下文），这个坑已经不存在了。本任务用一条测试把这个行为锁住，免得以后有人把 `context.WithValue(ctx, ...)` 写回来。

- [ ] **Step 1: 写测试**

新建 `sprout/context_test.go`：

```go
package sprout

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wxy365/basal/errs"
)

// TestContextLookupAfterEndpointError verifies that an interceptor that reads a
// context value after the handler failed does not deadlock.
//
// The endpoint error is recorded in the request context. The chain it is
// stored in must not be rooted at the request context itself, because the
// request context resolves its values by delegating to the very request it
// holds.
func TestContextLookupAfterEndpointError(t *testing.T) {
	looked := make(chan any, 1)
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return nil, errs.New("boom").WithStatus(http.StatusConflict)
		},
		Interceptors: []Interceptor{
			func(next func(ctx *requestContext) error) func(ctx *requestContext) error {
				return func(ctx *requestContext) error {
					err := next(ctx)
					// The interceptor reads a context value that is not the
					// endpoint error, after the handler has failed.
					go func() {
						looked <- ctx.Value(ctxKeyAcceptType)
					}()
					select {
					case v := <-looked:
						if v != MimeJson {
							t.Errorf("the accept type = %v, want %v", v, MimeJson)
						}
					case <-time.After(2 * time.Second):
						t.Error("looking up a context value after the endpoint error deadlocked")
					}
					return err
				}
			},
		},
	}

	rec := serveEndpoint(t, e, httptest.NewRequest(http.MethodGet, "/greet?name=foo", nil))
	if rec.Code != http.StatusConflict {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusConflict)
	}
}
```

- [ ] **Step 2: 运行测试，确认它通过**

Run: `cd sprout && env -u GOROOT go test -run TestContextLookupAfterEndpointError -v .`

Expected: PASS（`445999e` 已经消除了这个隐患，这条测试把它锁住）。

- [ ] **Step 3: 跑全量测试，确认没有回归**

Run: `cd sprout && env -u GOROOT go test ./...`

Expected: 全部 PASS。

- [ ] **Step 4: 提交**

```bash
cd sprout
git add context_test.go
git commit -m "test(context): guard the lookup of the values after the endpoint error"
```

---

### Task 2: SSE 事件编码

**Files:**
- Create: `sprout/sse.go`
- Test: `sprout/sse_test.go`（新建）

**Interfaces:**
- Consumes: `basal/json` 的 `Marshal(v any) ([]byte, error)`
- Produces:
  - `type sseEvent struct { ID string; Event string; Data any; Retry time.Duration }`
  - `func (e sseEvent) encode() ([]byte, error)` — 返回完整的 SSE 帧（含结尾空行）
  - `func sseData(data any) (string, error)`
  - `func sanitizeSSEField(value string) string`

- [ ] **Step 1: 写失败测试**

新建 `sprout/sse_test.go`：

```go
package sprout

import (
	"testing"
	"time"
)

// TestSSEEventEncode verifies the wire format of an SSE frame.
func TestSSEEventEncode(t *testing.T) {
	cases := []struct {
		name  string
		event sseEvent
		want  string
	}{
		{
			name:  "data only",
			event: sseEvent{Data: "hello"},
			want:  "data: hello\n\n",
		},
		{
			name:  "all the fields",
			event: sseEvent{ID: "42", Event: "tick", Data: "x", Retry: 3 * time.Second},
			want:  "id: 42\nevent: tick\nretry: 3000\ndata: x\n\n",
		},
		{
			name:  "an empty event still carries a data field",
			event: sseEvent{},
			want:  "data: null\n\n",
		},
		{
			name:  "a multi line payload is split into several data fields",
			event: sseEvent{Data: "a\nb"},
			want:  "data: a\ndata: b\n\n",
		},
		{
			name:  "the newlines of the field values are stripped",
			event: sseEvent{ID: "a\nb", Event: "c\r\nd", Data: "x"},
			want:  "id: ab\nevent: cd\ndata: x\n\n",
		},
		{
			name:  "a byte slice payload is written as is",
			event: sseEvent{Data: []byte("raw")},
			want:  "data: raw\n\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.event.encode()
			if err != nil {
				t.Fatalf("the event should be encoded, but got: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("the frame = %q, want %q", string(got), c.want)
			}
		})
	}
}

// TestSSEEventEncodeJSONPayload verifies that a structured payload is
// serialized as JSON.
func TestSSEEventEncodeJSONPayload(t *testing.T) {
	type payload struct {
		N int `json:"n"`
	}
	got, err := sseEvent{Data: payload{N: 7}}.encode()
	if err != nil {
		t.Fatalf("the event should be encoded, but got: %v", err)
	}
	want := "data: {\"n\":7}\n\n"
	if string(got) != want {
		t.Errorf("the frame = %q, want %q", string(got), want)
	}
}
```

- [ ] **Step 2: 运行测试，确认它失败**

Run: `cd sprout && env -u GOROOT go test -run TestSSEEvent -v .`

Expected: FAIL，编译错误 `undefined: sseEvent`。

- [ ] **Step 3: 实施最小实现**

新建 `sprout/sse.go`：

```go
package sprout

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	bjson "github.com/wxy365/basal/json"
)

// mimeEventStream is the MIME type for server-sent events.
const mimeEventStream = "text/event-stream"

// sseEvent represents a server-sent event.
//
// The fields map one to one onto the SSE protocol: ID is written as the "id"
// field, Event as the "event" field, Retry as the "retry" field and Data as
// the "data" field. The fields that carry no value are not written at all.
//
// The exported alias SSEEvent is declared in api.go, which collects the
// public API surface of the package.
type sseEvent struct {
	// ID is the identifier of the event. The client remembers the identifier
	// of the last event it received and sends it back in the Last-Event-ID
	// header when it reconnects.
	ID string
	// Event is the name of the event. The browser dispatches the event to the
	// listener registered for that name; an unnamed event is dispatched as a
	// plain message.
	Event string
	// Data is the payload of the event. A string or a byte slice is written as
	// is, any other value is serialized as JSON.
	Data any
	// Retry is the reconnection interval suggested to the client. It is
	// written in milliseconds.
	Retry time.Duration
}

// encode renders the event as an SSE frame. The fields are written in the
// order id, event, retry and data, and the frame ends with an empty line.
func (e sseEvent) encode() ([]byte, error) {
	data, err := sseData(e.Data)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if len(e.ID) > 0 {
		buf.WriteString("id: ")
		buf.WriteString(sanitizeSSEField(e.ID))
		buf.WriteByte('\n')
	}
	if len(e.Event) > 0 {
		buf.WriteString("event: ")
		buf.WriteString(sanitizeSSEField(e.Event))
		buf.WriteByte('\n')
	}
	if e.Retry > 0 {
		buf.WriteString("retry: ")
		buf.WriteString(strconv.FormatInt(e.Retry.Milliseconds(), 10))
		buf.WriteByte('\n')
	}
	for _, line := range strings.Split(data, "\n") {
		buf.WriteString("data: ")
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// sseData renders the payload of an event as text. A string and a byte slice
// are returned as they are, any other value is serialized as JSON. An empty
// payload is rendered as "null" rather than as an empty string, because the
// browser does not dispatch an event whose data buffer is empty.
func sseData(data any) (string, error) {
	switch v := data.(type) {
	case nil:
		return "null", nil
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	}
	raw, err := bjson.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// sanitizeSSEField strips the newlines out of a field value. The SSE protocol
// does not allow a field value to contain a newline, because it would break
// the structure of the frame.
func sanitizeSSEField(value string) string {
	if !strings.ContainsAny(value, "\r\n") {
		return value
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(value)
}
```

- [ ] **Step 4: 运行测试，确认它通过**

Run: `cd sprout && env -u GOROOT go test -run TestSSEEvent -v .`

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add sse.go sse_test.go
git commit -m "feat(sse): add the server-sent event wire format"
```

---

### Task 3: SSE 流句柄

**Files:**
- Modify: `sprout/sse.go`（追加到文件末尾）
- Test: `sprout/sse_test.go`（追加）

**Interfaces:**
- Consumes: Task 2 的 `sseEvent.encode()`、`sseData()`、`sanitizeSSEField()`
- Produces:
  - `type sseStream struct`（内部字段全部非导出）
  - `func newSSEStream(ctx *requestContext, keepAlive time.Duration) *sseStream`
  - `func (s *sseStream) Send(ev sseEvent) error`
  - `func (s *sseStream) SendData(data any) error`
  - `func (s *sseStream) SendEvent(name string, data any) error`
  - `func (s *sseStream) Comment(text string) error`
  - `func (s *sseStream) Close() error`
  - `func (s *sseStream) Done() <-chan struct{}`
  - `func (s *sseStream) Err() error`
  - `func (s *sseStream) isCommitted() bool`
  - `func (s *sseStream) sendError(err error) error`
  - `var errSSEClosed error`

- [ ] **Step 1: 写失败测试**

追加到 `sprout/sse_test.go`：

```go
// newTestSSEStream builds a stream that writes into the given recorder.
func newTestSSEStream(t *testing.T, rec *httptest.ResponseRecorder) *sseStream {
	t.Helper()
	req := newTestRequest(http.MethodGet, "/sse", "")
	ctx := &requestContext{Request: req, Writer: rec}
	return newSSEStream(ctx, 0)
}

// TestSSEStreamSend verifies that a sent event reaches the response and that
// the response is committed on the first write.
func TestSSEStreamSend(t *testing.T) {
	rec := newResponseRecorder()
	stream := newTestSSEStream(t, rec)

	if err := stream.Send(sseEvent{Data: "hello"}); err != nil {
		t.Fatalf("the event should be sent, but got: %v", err)
	}
	if rec.Body.String() != "data: hello\n\n" {
		t.Errorf("the body = %q, want %q", rec.Body.String(), "data: hello\n\n")
	}
	if got := rec.Header().Get("Content-Type"); got != mimeEventStream {
		t.Errorf("the Content-Type header = %q, want %q", got, mimeEventStream)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("the Cache-Control header = %q, want %q", got, "no-cache")
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("the X-Accel-Buffering header = %q, want %q", got, "no")
	}
	if !rec.Flushed {
		t.Error("the response should be flushed after an event is written")
	}
	if !stream.isCommitted() {
		t.Error("the stream should be committed after an event is written")
	}
}

// TestSSEStreamCloseCommitsTheResponse verifies that closing a stream that has
// not written anything still commits an empty but well formed response.
func TestSSEStreamCloseCommitsTheResponse(t *testing.T) {
	rec := newResponseRecorder()
	stream := newTestSSEStream(t, rec)

	if err := stream.Close(); err != nil {
		t.Fatalf("the stream should be closed, but got: %v", err)
	}
	if got := rec.Header().Get("Content-Type"); got != mimeEventStream {
		t.Errorf("the Content-Type header = %q, want %q", got, mimeEventStream)
	}
	if len(rec.Body.Bytes()) != 0 {
		t.Errorf("the body should be empty, but got: %q", rec.Body.String())
	}
}

// TestSSEStreamSendAfterClose verifies that writing to a closed stream fails
// instead of silently dropping the event.
func TestSSEStreamSendAfterClose(t *testing.T) {
	rec := newResponseRecorder()
	stream := newTestSSEStream(t, rec)
	stream.Close()

	if err := stream.Send(sseEvent{Data: "hello"}); err == nil {
		t.Error("sending to a closed stream should fail")
	}
}

// TestSSEStreamComment verifies that a comment is written as a comment line,
// which the browser ignores.
func TestSSEStreamComment(t *testing.T) {
	rec := newResponseRecorder()
	stream := newTestSSEStream(t, rec)

	if err := stream.Comment("ping"); err != nil {
		t.Fatalf("the comment should be written, but got: %v", err)
	}
	if rec.Body.String() != ": ping\n\n" {
		t.Errorf("the body = %q, want %q", rec.Body.String(), ": ping\n\n")
	}
}

// TestSSEStreamDisconnect verifies that the stream reports the disconnection
// of the client.
func TestSSEStreamDisconnect(t *testing.T) {
	req := newTestRequest(http.MethodGet, "/sse", "")
	cancellable, cancel := context.WithCancel(req.Context())
	req = req.WithContext(cancellable)
	rec := newResponseRecorder()
	stream := newSSEStream(&requestContext{Request: req, Writer: rec}, 0)

	cancel()

	select {
	case <-stream.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the stream should report the disconnection of the client")
	}
	if stream.Err() == nil {
		t.Error("the stream should report why it ended")
	}
}

// TestSSEStreamKeepAlive verifies that the keep alive comments are written
// while the stream is open.
func TestSSEStreamKeepAlive(t *testing.T) {
	req := newTestRequest(http.MethodGet, "/sse", "")
	rec := newResponseRecorder()
	stream := newSSEStream(&requestContext{Request: req, Writer: rec}, 20*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.Body.String(), ": ping") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stream.Close()

	if !strings.Contains(rec.Body.String(), ": ping") {
		t.Errorf("the keep alive comment is missing, but got: %q", rec.Body.String())
	}
}

// TestSSEStreamSurvivesTheWriteTimeout verifies that the stream outlives the
// write timeout of the server, which would otherwise cut a long connection
// short.
func TestSSEStreamSurvivesTheWriteTimeout(t *testing.T) {
	svr := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := &requestContext{Request: r, Writer: w}
			stream := newSSEStream(ctx, 0)
			defer stream.Close()
			for i := 0; i < 4; i++ {
				if err := stream.Send(sseEvent{Data: "tick"}); err != nil {
					return
				}
				time.Sleep(300 * time.Millisecond)
			}
		}),
		WriteTimeout: 500 * time.Millisecond,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("the listener should be created, but got: %v", err)
	}
	defer ln.Close()
	go svr.Serve(ln)
	defer svr.Close()

	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("the request should be served, but got: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("the body should be read, but got: %v", err)
	}
	// The four ticks are 300 milliseconds apart, so the stream lives for about
	// 1.2 seconds, well beyond the write timeout of 500 milliseconds. Without
	// the write deadline being cleared, the third write would fail.
	if got := strings.Count(string(body), "data: tick\n\n"); got != 4 {
		t.Errorf("the number of the events = %d, want %d", got, 4)
	}
}

// TestSSEStreamConcurrentSend verifies that the concurrent writes of several
// goroutines do not interleave the frames.
func TestSSEStreamConcurrentSend(t *testing.T) {
	rec := newResponseRecorder()
	stream := newTestSSEStream(t, rec)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := stream.Send(sseEvent{Data: "0123456789"}); err != nil {
					t.Errorf("the event should be sent, but got: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	body := rec.Body.String()
	if got := strings.Count(body, "data: 0123456789\n\n"); got != 80 {
		t.Errorf("the number of the complete frames = %d, want %d", got, 80)
	}
}
```

同时把 `sprout/sse_test.go` 的 import 块改成：

```go
import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)
```

- [ ] **Step 2: 运行测试，确认它失败**

Run: `cd sprout && env -u GOROOT go test -run TestSSEStream -v .`

Expected: FAIL，编译错误 `undefined: sseStream`。

- [ ] **Step 3: 实施最小实现**

追加到 `sprout/sse.go`：

```go
// errSSEClosed is returned when an event is written to a stream that has
// already ended.
var errSSEClosed = errors.New("The SSE stream has been closed")

// sseStream writes server-sent events to an HTTP response.
//
// The framework creates the stream before it calls the business logic and
// hands it over as the third argument of the handler. The writes are
// serialized by a mutex, so the keep alive goroutine and the handler may call
// Send at the same time.
//
// The exported alias SSEStream is declared in api.go, which collects the
// public API surface of the package.
type sseStream struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
	// requestCtx is the context of the request that opened the stream. It is
	// cancelled when the client disconnects.
	requestCtx context.Context
	// protoMajor is the major version of the HTTP protocol of the request.
	// Only HTTP/1.1 may carry the Connection header.
	protoMajor int

	mu        sync.Mutex
	committed bool
	closed    bool
	err       error
	done      chan struct{}
	closeOnce sync.Once
}

// newSSEStream builds a stream that writes into the response of the given
// request. A keep alive interval greater than zero starts the goroutine that
// keeps the connection alive.
func newSSEStream(ctx *requestContext, keepAlive time.Duration) *sseStream {
	s := &sseStream{
		writer:     ctx.Writer,
		controller: http.NewResponseController(ctx.Writer),
		requestCtx: ctx.Request.Context(),
		protoMajor: ctx.Request.ProtoMajor,
		done:       make(chan struct{}),
	}
	// The write timeout of the server cuts a long connection short, so it is
	// cleared here. The writers of HTTP/1.1, HTTP/2 and HTTP/3 all support
	// it.
	if err := s.controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.WarnErrF("Failed to clear the write deadline of the SSE stream", err)
	}
	go s.watchDisconnect()
	if keepAlive > 0 {
		go s.keepAlive(keepAlive)
	}
	return s
}

// Send writes an event to the stream. The response is committed on the first
// write.
func (s *sseStream) Send(ev sseEvent) error {
	frame, err := ev.encode()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errSSEClosed
	}
	s.commitLocked()
	if _, err := s.writer.Write(frame); err != nil {
		s.failLocked(err)
		return err
	}
	if err := s.controller.Flush(); err != nil {
		s.failLocked(err)
		return err
	}
	return nil
}

// SendData writes an event that carries nothing but a payload.
func (s *sseStream) SendData(data any) error {
	return s.Send(sseEvent{Data: data})
}

// SendEvent writes a named event.
func (s *sseStream) SendEvent(name string, data any) error {
	return s.Send(sseEvent{Event: name, Data: data})
}

// Comment writes a comment line. A comment is invisible to the browser, so it
// only keeps the connection alive.
func (s *sseStream) Comment(text string) error {
	line := ": " + sanitizeSSEField(text) + "\n\n"
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errSSEClosed
	}
	s.commitLocked()
	if _, err := s.writer.Write([]byte(line)); err != nil {
		s.failLocked(err)
		return err
	}
	if err := s.controller.Flush(); err != nil {
		s.failLocked(err)
		return err
	}
	return nil
}

// Close ends the stream. A stream that has not written anything yet is
// committed on the way out, so that the client receives a well formed but
// empty response.
func (s *sseStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.commitLocked()
	s.closed = true
	s.closeOnce.Do(func() { close(s.done) })
	return nil
}

// Done is closed when the client disconnects, when a write fails or when the
// stream is closed. The handler should watch it to leave its loop in time.
func (s *sseStream) Done() <-chan struct{} {
	return s.done
}

// Err reports why the stream ended: the error of the request context when the
// client disconnected, the underlying I/O error when a write failed, and nil
// when the stream was closed on purpose.
func (s *sseStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// isCommitted reports whether the response has been committed. Once it is,
// the status code can no longer be changed, so an error can only be reported
// as an event.
func (s *sseStream) isCommitted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.committed
}

// sendError reports an error as an error event. It is only useful once the
// response has been committed.
func (s *sseStream) sendError(err error) error {
	payload := map[string]any{"message": err.Error()}
	var e *errs.Err
	if errors.As(err, &e) {
		if len(e.Code) > 0 {
			payload["code"] = e.Code
		}
		if len(e.Message) > 0 {
			payload["message"] = e.Message
		}
		if e.Status > 0 {
			payload["status"] = e.Status
		}
	}
	return s.Send(sseEvent{Event: "error", Data: payload})
}

// commitLocked writes the headers of the SSE response. The response is
// committed lazily: the headers go out only when a frame is really written, so
// that an error that happens before that still gets the right status code.
// The caller must hold s.mu.
func (s *sseStream) commitLocked() {
	if s.committed {
		return
	}
	header := s.writer.Header()
	header.Set("Content-Type", mimeEventStream)
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	if s.protoMajor == 1 {
		// HTTP/2 and HTTP/3 do not allow the connection specific headers.
		header.Set("Connection", "keep-alive")
	}
	s.writer.WriteHeader(http.StatusOK)
	s.committed = true
}

// failLocked ends the stream with the given error. The caller must hold s.mu.
func (s *sseStream) failLocked(err error) {
	if s.closed {
		return
	}
	s.closed = true
	if err != nil && s.err == nil {
		s.err = err
	}
	s.closeOnce.Do(func() { close(s.done) })
}

// watchDisconnect waits for the client to disconnect. The request context is
// cancelled when the client goes away and when the handler returns, so this
// goroutine always ends.
func (s *sseStream) watchDisconnect() {
	<-s.requestCtx.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failLocked(s.requestCtx.Err())
}

// keepAlive writes a comment line at the given interval, so that the proxies
// in between do not drop an idle connection.
func (s *sseStream) keepAlive(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.Comment("ping"); err != nil {
				return
			}
		case <-s.done:
			return
		}
	}
}
```

同时把 `sprout/sse.go` 的 import 块补成：

```go
import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wxy365/basal/errs"
	bjson "github.com/wxy365/basal/json"
	"github.com/wxy365/basal/log"
)
```

- [ ] **Step 4: 运行测试，确认它通过**

Run: `cd sprout && env -u GOROOT go test -run TestSSEStream -v .`

Expected: PASS。

- [ ] **Step 5: 跑竞态检测**

Run: `cd sprout && env -u GOROOT go test -race -run TestSSEStream .`

Expected: PASS，无数据竞争报告。

- [ ] **Step 6: 提交**

```bash
cd sprout
git add sse.go sse_test.go
git commit -m "feat(sse): add the SSE stream that writes and flushes the events"
```

---

### Task 4: SSE 端点接入框架

**Files:**
- Modify: `sprout/sse.go`（追加到文件末尾）
- Modify: `sprout/context.go`（`ctxValues` 加一个字段，加两个访问方法）
- Test: `sprout/sse_test.go`（追加）

**Interfaces:**
- Consumes: Task 3 的 `newSSEStream`、`sseStream`、`errSSEClosed`；既有的 `parseHttpRequest`、`svr.buildInputEntityValidateFuncs`、`appendServerInterceptors`、`refinedEndpoint`、`requestContext.setEndpointError`
- Produces:
  - `type sseHandlerFunc[I any] func(ctx *requestContext, in I, stream *sseStream) error`
  - `type sseEndpoint[I any] struct { Name string; Pattern string; Methods []string; Handler sseHandlerFunc[I]; Interceptors []interceptorFunc; ErrorHandler errorHandlerFunc; KeepAlive time.Duration }`
  - `func (e *sseEndpoint[I]) appendToServer(svr *httpServer, decrypters map[string]func(cipher []byte) ([]byte, error))`
  - `ctxValues.sseStream *sseStream` 字段（`sprout/context.go`）
  - `func (c *requestContext) setSSEStream(stream *sseStream)`
  - `func (c *requestContext) sseStreamOf() *sseStream`

- [ ] **Step 1: 写失败测试**

追加到 `sprout/sse_test.go`：

```go
// sseInput is the input entity used by the SSE endpoint tests.
type sseInput struct {
	Name  string `query:"name"`
	Count int    `query:"count" validate:"[1,10]"`
}

// serveSSE mounts the given SSE endpoint on a fresh server and serves one
// request against it.
func serveSSE[I any](t *testing.T, e *sseEndpoint[I], req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	svr := newDefaultServer("test")
	e.appendToServer(svr, nil)
	mx := svr.buildMux()
	rec := httptest.NewRecorder()
	mx.ServeHTTP(rec, req)
	return rec
}

// assertSSEBody verifies that every line of the body belongs to an SSE frame,
// so that a plain JSON error never leaks into the stream.
func assertSSEBody(t *testing.T, body string) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if len(line) == 0 {
			continue
		}
		ok := strings.HasPrefix(line, "data: ") ||
			strings.HasPrefix(line, "event: ") ||
			strings.HasPrefix(line, "id: ") ||
			strings.HasPrefix(line, "retry: ") ||
			strings.HasPrefix(line, ": ")
		if !ok {
			t.Errorf("the line %q is not part of an SSE frame", line)
		}
	}
}

// TestSSEEndpointSuccess verifies the happy path of an SSE endpoint.
func TestSSEEndpointSuccess(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			return stream.Send(sseEvent{Event: "tick", Data: "hello " + in.Name})
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse?name=foo", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("the status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != mimeEventStream {
		t.Errorf("the Content-Type header = %q, want %q", got, mimeEventStream)
	}
	want := "event: tick\ndata: hello foo\n\n"
	if rec.Body.String() != want {
		t.Errorf("the body = %q, want %q", rec.Body.String(), want)
	}
}

// TestSSEEndpointLazyCommit verifies that an endpoint that writes nothing
// still answers with a well formed but empty SSE response.
func TestSSEEndpointLazyCommit(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			return nil
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("the status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != mimeEventStream {
		t.Errorf("the Content-Type header = %q, want %q", got, mimeEventStream)
	}
	if len(rec.Body.Bytes()) != 0 {
		t.Errorf("the body should be empty, but got: %q", rec.Body.String())
	}
}

// TestSSEEndpointErrorBeforeCommit verifies that an error that happens before
// the first event is reported with its own status code and a plain JSON body.
func TestSSEEndpointErrorBeforeCommit(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			return errs.New("something went wrong").WithStatus(http.StatusConflict)
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse", nil))

	if rec.Code != http.StatusConflict {
		t.Fatalf("the status code = %d, want %d", rec.Code, http.StatusConflict)
	}
	if !strings.Contains(rec.Body.String(), "something went wrong") {
		t.Errorf("the body should contain the error message, but got: %s", rec.Body.String())
	}
}

// TestSSEEndpointErrorAfterCommit verifies that an error that happens after
// the first event is reported as an error event, because the status code can
// no longer be changed.
func TestSSEEndpointErrorAfterCommit(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			if err := stream.Send(sseEvent{Data: "before"}); err != nil {
				return err
			}
			return errs.New("something went wrong").WithStatus(http.StatusConflict)
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("the status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "data: before\n\n") {
		t.Errorf("the first event is missing, but got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "event: error\n") {
		t.Errorf("the error event is missing, but got: %s", rec.Body.String())
	}
	assertSSEBody(t, rec.Body.String())
}

// TestSSEEndpointPanicRecovered verifies that a panic in the handler becomes
// an error event instead of a JSON body mixed into the stream.
func TestSSEEndpointPanicRecovered(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			if err := stream.Send(sseEvent{Data: "before"}); err != nil {
				return err
			}
			panic("boom")
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse", nil))

	if !strings.Contains(rec.Body.String(), "event: error\n") {
		t.Errorf("the error event is missing, but got: %s", rec.Body.String())
	}
	assertSSEBody(t, rec.Body.String())
}

// TestSSEEndpointValidationFailure verifies that an invalid input is rejected
// before the stream is opened.
func TestSSEEndpointValidationFailure(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			t.Error("the handler should not be called for an invalid input")
			return nil
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse?count=99", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
```

- [ ] **Step 2: 运行测试，确认它失败**

Run: `cd sprout && env -u GOROOT go test -run TestSSEEndpoint -v .`

Expected: FAIL，编译错误 `undefined: sseEndpoint`。

- [ ] **Step 3: 实施最小实现**

追加到 `sprout/sse.go`：

```go
// sseHandlerFunc is the signature of the business logic of an SSE endpoint.
// It writes the events to the stream synchronously and ends the connection
// when it returns.
//
// The exported alias SSEHandler is declared in api.go, which collects the
// public API surface of the package.
type sseHandlerFunc[I any] func(ctx *requestContext, in I, stream *sseStream) error

// sseEndpoint defines an SSE endpoint with its pattern, HTTP methods,
// business logic handler, interceptors and error handler.
//
// The exported alias SSEEndpoint is declared in api.go, which collects the
// public API surface of the package.
type sseEndpoint[I any] struct {
	// Name is the endpoint name, used for logging and circuit breaker identification.
	Name string
	// Pattern is the URI path pattern, supporting static segments, named parameters
	// (e.g. {id}), regex parameters (e.g. {name:~re}), format specifiers (%s, %d),
	// and wildcards (*).
	Pattern string
	// Methods is the list of HTTP methods this endpoint responds to (e.g. GET, POST).
	Methods []string
	// Handler is the business logic function that writes the events.
	Handler sseHandlerFunc[I]
	// Interceptors is the list of interceptors applied to this endpoint. Built-in
	// interceptors (panic recovery, circuit breaker, rate limiter, CORS) are added
	// automatically during registration.
	Interceptors []interceptorFunc
	// ErrorHandler is the error handler for this endpoint. If nil, the server-level
	// error handler is used as a fallback.
	ErrorHandler errorHandlerFunc
	// KeepAlive is the interval of the keep alive comments. It is greater than
	// zero when the endpoint keeps its connection alive. Zero turns it off.
	KeepAlive time.Duration
}

func (e *sseEndpoint[I]) appendToServer(svr *httpServer, decrypters map[string]func(cipher []byte) ([]byte, error)) {
	// validate endpoint methods
	for _, mth := range e.Methods {
		if slices.Lookup(allowedMethods, mth, func(left, right string) bool {
			return left == right
		}) < 0 {
			panic("Invalid http method: " + mth)
		}
	}

	// validate endpoint input type
	var input I
	inputType := reflect.TypeOf(input)
	for inputType.Kind() == reflect.Pointer {
		inputType = inputType.Elem()
	}
	if inputType.Kind() != reflect.Struct {
		log.Panic("SSE endpoint ({0}) input type must be a struct, but got: [{1}]", e.Name, inputType)
	}

	r := &refinedEndpoint{
		name:    e.Name,
		pattern: e.Pattern,
		methods: e.Methods,
	}

	validateFunc := svr.buildInputEntityValidateFuncs(inputType)

	errHandler := e.ErrorHandler
	if errHandler == nil {
		errHandler = svr.ErrorHandler
	}
	if errHandler == nil {
		errHandler = defaultErrHandler
	}

	httpHandler := func(ctx *requestContext) error {
		var in I
		err := parseHttpRequest(&in, ctx.Request, decrypters)
		if err != nil {
			return errs.Wrap(err, "Failed to parse request of endpoint [{0}]", e.Name).WithStatus(http.StatusBadRequest)
		}
		err = validateFunc(ctx, reflect.ValueOf(in))
		if err != nil {
			return err
		}

		if svr.Debug {
			inputStr, _ := bjson.Marshal(in)
			log.Debug(`SSE endpoint [{0}] input: {1}`, r.name, string(inputStr))
		}

		stream := newSSEStream(ctx, e.KeepAlive)
		// The stream is recorded in the values of the request, so that the
		// error handler can tell whether the response has been committed.
		ctx.setSSEStream(stream)

		handlerErr := callSSEHandler(e.Handler, ctx, in, stream)
		if handlerErr != nil {
			// The error is recorded in the values of the request, so that the
			// circuit breaker can tell that the request failed.
			ctx.setEndpointError(handlerErr)
			if stream.isCommitted() {
				// The response has been committed, so the status code can no
				// longer be changed and the error is reported as an event.
				stream.sendError(handlerErr)
				stream.Close()
				return nil
			}
			return handlerErr
		}
		stream.Close()
		return nil
	}

	ics := []interceptorFunc{recoverInterceptor}
	ics = append(ics, newCircuitBreakerInterceptor(r.name))
	ics = append(ics, newRateLimiterInterceptor(r.name))
	corsInterceptor := newCorsInterceptor()
	if corsInterceptor != nil {
		ics = append(ics, corsInterceptor)
	}
	// The interceptors of the endpoint are applied after the built-in ones, so
	// that the built-in panic recovery stays the outermost interceptor.
	ics = append(ics, e.Interceptors...)
	httpHandler = appendServerInterceptors(httpHandler, ics...)
	r.httpHandler = func(ctx *requestContext) error {
		err := httpHandler(ctx)
		if err != nil {
			if stream := ctx.sseStreamOf(); stream != nil && stream.isCommitted() {
				// The response has been committed, so the error is reported as
				// an event instead of a status code.
				stream.sendError(err)
				stream.Close()
				return nil
			}
			errHandler(ctx, err)
		}
		return nil
	}

	svr.endpoints = append(svr.endpoints, r)
}

// callSSEHandler calls the business logic of an SSE endpoint and turns a
// panic into an error, so that a panic never leaks a plain JSON body into
// the stream.
func callSSEHandler[I any](handler sseHandlerFunc[I], ctx *requestContext, in I, stream *sseStream) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic recovered in SSE endpoint: {0} {1}", ctx.Request.Method, ctx.Request.URL.Path)
			err = errs.New("Internal Server Error").WithStatus(http.StatusInternalServerError)
		}
	}()
	return handler(ctx, in, stream)
}
```

同时把 `sprout/sse.go` 的 import 块补成：

```go
import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wxy365/basal/ds/slices"
	"github.com/wxy365/basal/errs"
	bjson "github.com/wxy365/basal/json"
	"github.com/wxy365/basal/log"
)
```

同时修改 `sprout/context.go`。框架自带的值都装在 `ctxValues` 里，SSE 的流也放进去，跟 `endpointErr` 一个层级：

在 `ctxValues` 结构体里，`endpointErr` 字段之后加上：

```go
	// sseStream is the stream of the SSE endpoint that is serving the
	// request. It is nil for the requests that are not served by an SSE
	// endpoint.
	sseStream *sseStream
```

在 `setEndpointError` 方法之后加上：

```go
// setSSEStream records the stream of the SSE endpoint that is serving the
// request, so that the error handler can tell whether the response has already
// been committed.
func (c *requestContext) setSSEStream(stream *sseStream) {
	c.values.sseStream = stream
}

// sseStreamOf returns the stream of the SSE endpoint that is serving the
// request, or nil when the request is not served by an SSE endpoint.
func (c *requestContext) sseStreamOf() *sseStream {
	return c.values.sseStream
}
```

- [ ] **Step 4: 运行测试，确认它通过**

Run: `cd sprout && env -u GOROOT go test -run TestSSEEndpoint -v .`

Expected: PASS。

- [ ] **Step 5: 跑全量测试与竞态检测**

Run: `cd sprout && env -u GOROOT go test -race ./...`

Expected: 全部 PASS，无数据竞争报告。

- [ ] **Step 6: 提交**

```bash
cd sprout
git add sse.go sse_test.go
git commit -m "feat(sse): wire the SSE endpoint into the endpoint chain"
```

---

### Task 5: 挂载与公共 API

**Files:**
- Modify: `sprout/app.go:199-228`（`mount` 拆出共用的 `mountEndpoint`）
- Modify: `sprout/api.go`（类型别名、`MountSSE`、`MimeEventStream`）
- Test: `sprout/public_api_test.go`（外部测试包，只用公共 API）

**Interfaces:**
- Consumes: Task 4 的 `sseEndpoint[I].appendToServer`
- Produces:
  - `func mountEndpoint[C any](a *App[C], svrName []string, endpointName string, appendToServer func(*httpServer, map[string]func(cipher []byte) ([]byte, error)))`
  - `func mountSSE[C any, I any](e *SSEEndpoint[I], a *App[C], svrName ...string)`（在 `app.go`）
  - `func MountSSE[C any, I any](e *SSEEndpoint[I], a *App[C], svrName ...string)`（在 `api.go`，再导出）
  - `api.go` 中的别名：`SSEEvent = sseEvent`、`SSEStream = sseStream`、`SSEHandler[I any] = sseHandlerFunc[I]`、`SSEEndpoint[I any] = sseEndpoint[I]`
  - `api.go` 中的常量：`MimeEventStream = mimeEventStream`

- [ ] **Step 1: 写失败测试**

追加到 `sprout/public_api_test.go`：

```go
// publicApiSSEHandler is a business logic function of an SSE endpoint written
// against the public API only.
func publicApiSSEHandler(ctx *sprout.Context, in publicApiReq, stream *sprout.SSEStream) error {
	return stream.Send(sprout.SSEEvent{Event: "item", Data: in.ID})
}

// TestPublicApiSSENames checks that the exported names of the SSE support
// keep their meaning and their signature.
func TestPublicApiSSENames(t *testing.T) {
	var (
		_ sprout.SSEEvent
		_ *sprout.SSEStream
		_ sprout.SSEHandler[publicApiReq]
		_ *sprout.SSEEndpoint[publicApiReq]
	)
	_ = sprout.MimeEventStream
}

// TestPublicApiMountSSE checks that an SSE endpoint can be declared and
// mounted through the public API only.
func TestPublicApiMountSSE(t *testing.T) {
	app := &sprout.App[*publicApiCtx]{Name: "public-api-test"}

	endpoint := &sprout.SSEEndpoint[publicApiReq]{
		Name:    "PublicApiSSE",
		Pattern: "/items/{id}/events",
		Methods: []string{http.MethodGet},
		Handler: publicApiSSEHandler,
	}
	sprout.MountSSE(endpoint, app)

	if len(app.Initializers) != 1 {
		t.Fatalf("the SSE endpoint has not been mounted: %d initializer(s)", len(app.Initializers))
	}
}
```

- [ ] **Step 2: 运行测试，确认它失败**

Run: `cd sprout && env -u GOROOT go test -run TestPublicApiSSE -v .`

Expected: FAIL，编译错误 `undefined: sprout.SSEEvent`（以及 `sprout.SSEStream`、`sprout.SSEHandler`、`sprout.SSEEndpoint`、`sprout.MountSSE`、`sprout.MimeEventStream`）。

- [ ] **Step 3: 在 app.go 里抽出共用的挂载逻辑**

修改 `sprout/app.go` 的 `mount` 函数，把重复的挂载逻辑抽到 `mountEndpoint`：

```go
// mount registers an endpoint onto a server within the application.
//
// The exported wrapper Mount is declared in api.go, which collects the public
// API surface of the package.
func mount[C any, I any, O any](e *Endpoint[I, O], a *App[C], svrName ...string) {
	mountEndpoint(a, svrName, e.Name, e.appendToServer)
}

// mountSSE registers an SSE endpoint onto a server within the application.
//
// The exported wrapper MountSSE is declared in api.go, which collects the
// public API surface of the package.
func mountSSE[C any, I any](e *SSEEndpoint[I], a *App[C], svrName ...string) {
	mountEndpoint(a, svrName, e.Name, e.appendToServer)
}

// mountEndpoint wires an endpoint onto a server within the application. The
// endpoint is wired up via an AppInitializer, ensuring it is registered before
// the server starts. If no server name is provided, the endpoint is mounted on
// the first server in the App.Servers slice.
//
// The appendToServer is the registration logic of the endpoint itself, which
// the ordinary endpoints and the SSE endpoints provide on their own.
func mountEndpoint[C any](a *App[C], svrName []string, endpointName string,
	appendToServer func(svr *httpServer, decrypters map[string]func(cipher []byte) ([]byte, error))) {
	a.Initializers = append(a.Initializers, func(app *App[C]) error {
		if len(svrName) == 0 {
			if len(app.Servers) == 0 {
				app.Servers = append(app.Servers, newDefaultServer(app.Name+"_server"))
			}
			appendToServer(app.Servers[0], app.DecryptFns)
		} else {
			var mounted bool
			for _, svr := range app.Servers {
				if svr.Name == svrName[0] {
					appendToServer(svr, app.DecryptFns)
					mounted = true
					break
				}
			}
			if !mounted {
				log.Panic("Failed to mount endpoint [{0}] to server [{1}]: server not defined", endpointName, svrName[0])
			}
		}
		return nil
	})
}
```

- [ ] **Step 4: 在 api.go 里补上公共 API**

在 `sprout/api.go` 的 `type (...)` 块里追加：

```go
	// SSEEvent represents a server-sent event.
	//
	// See sse.go for the implementation.
	SSEEvent = sseEvent
	// SSEStream writes server-sent events to an HTTP response.
	//
	// See sse.go for the implementation.
	SSEStream = sseStream
	// SSEHandler is the function signature for the business logic of an SSE
	// endpoint. It receives a request context, a deserialized input struct and
	// the stream the events are written to.
	//
	// See sse.go for the implementation.
	SSEHandler[I any] = sseHandlerFunc[I]
	// SSEEndpoint defines an SSE endpoint with its pattern, HTTP methods,
	// business logic handler, interceptors and error handler.
	//
	// See sse.go for the implementation.
	SSEEndpoint[I any] = sseEndpoint[I]
```

在 `const (...)` 块里追加：

```go
	// MimeEventStream is the MIME type for server-sent events.
	MimeEventStream = mimeEventStream
```

在文件末尾追加：

```go
// MountSSE registers an SSE endpoint onto a server within the application.
// The endpoint is wired up via an AppInitializer, ensuring it is registered
// before the server starts. If no server name is provided, the endpoint is
// mounted on the first server in the App.Servers slice.
//
// See app.go for the implementation.
func MountSSE[C any, I any](e *SSEEndpoint[I], a *App[C], svrName ...string) {
	mountSSE(e, a, svrName...)
}
```

- [ ] **Step 5: 运行测试，确认它通过**

Run: `cd sprout && env -u GOROOT go test -run TestPublicApi -v .`

Expected: PASS。

- [ ] **Step 6: 跑全量测试**

Run: `cd sprout && env -u GOROOT go test ./...`

Expected: 全部 PASS。

- [ ] **Step 7: 提交**

```bash
cd sprout
git add app.go api.go public_api_test.go
git commit -m "feat(sse): expose the SSE endpoint through the public API"
```

---

### Task 6: mimo 演示端点

**Files:**
- Create: `mimo/iface/sse/sse.go`
- Modify: `mimo/api_test.http`

**Interfaces:**
- Consumes: Task 5 的 `sprout.MountSSE`、`sprout.SSEEndpoint[I]`、`sprout.SSEHandler[I]`、`sprout.SSEEvent`、`sprout.SSEStream`
- Produces: `GET /sse/tick?interval=&count=`

**背景：** 框架的绑定顺序是 path > default > query > header > cookie（见 `sprout/parse_request_test.go` 第 109 到 114 行），带 `default` 标签的字段会忽略 query 参数。因此入参**不用** `default` 标签，改用 0 作「未指定」哨兵：校验区间把 0 视为合法，处理函数再把 0 映射成默认值。这样 query 参数能正常生效，也不必改动框架的既定语义。

- [ ] **Step 1: 写演示端点**

新建 `mimo/iface/sse/sse.go`：

```go
package sse

import (
	"fmt"
	"net/http"
	"time"

	"mimo/app_ctx"

	"github.com/wxy365/sprout"
)

// TickReq is the input entity of the SSE demonstration endpoint.
type TickReq struct {
	// Interval is the interval between two events, in seconds.
	// The value 0 means that the default interval is used.
	Interval int `query:"interval" validate:"[0,60]"`
	// Count is the number of the events to push.
	// The value 0 means that the default count is used.
	Count int `query:"count" validate:"[0,100]"`
}

// TickResp is the payload of every pushed event.
type TickResp struct {
	Seq int    `json:"seq"`
	TS  string `json:"ts"`
}

// Tick pushes an event at a fixed interval, until the events are exhausted or
// the client disconnects.
func Tick(ctx *sprout.Context, req TickReq, stream *sprout.SSEStream) error {
	interval := req.Interval
	if interval == 0 {
		interval = 1
	}
	count := req.Count
	if count == 0 {
		count = 10
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for seq := 1; seq <= count; seq++ {
		select {
		case <-stream.Done():
			return stream.Err()
		case <-ticker.C:
		}
		err := stream.Send(sprout.SSEEvent{
			ID:    fmt.Sprintf("%d", seq),
			Event: "tick",
			Data: TickResp{
				Seq: seq,
				TS:  time.Now().Format(time.RFC3339),
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func init() {
	sprout.MountSSE(&sprout.SSEEndpoint[TickReq]{
		Name:    "Tick",
		Pattern: "/sse/tick",
		Methods: []string{http.MethodGet},
		Handler: Tick,
	}, app_ctx.App, "pub")
}
```

- [ ] **Step 2: 补一条 curl 示例**

在 `mimo/api_test.http` 末尾追加：

```
### 订阅服务端推送的事件（SSE）
GET {{host}}/sse/tick?interval=1&count=10
Accept: text/event-stream
```

- [ ] **Step 3: 构建 mimo，确认它编译通过**

Run: `cd mimo && env -u GOROOT go build ./... && env -u GOROOT go vet ./...`

Expected: 无输出（成功）。

- [ ] **Step 4: 跑起来，用 curl 验证**

Run（后台启动 mimo，然后请求）：

```bash
cd mimo
env -u GOROOT go run . &
sleep 3
curl -N -s "http://localhost:80/sse/tick?interval=1&count=3"
```

Expected: 输出三帧，形如：

```
id: 1
event: tick
data: {"seq":1,"ts":"2026-11-17T12:00:00+08:00"}

id: 2
event: tick
data: {"seq":2,"ts":"2026-11-17T12:00:01+08:00"}

id: 3
event: tick
data: {"seq":3,"ts":"2026-11-17T12:00:02+08:00"}

```

验证完把 mimo 进程停掉。

**注意：** mimo 默认监听 80 端口。如果 80 端口被占用，改 `mimo/config/config.json` 里 `app.servers[0].port` 的临时值（**验证后必须还原**，不要把这个改动提交）。

- [ ] **Step 5: 提交**

```bash
cd mimo
git add iface/sse/sse.go api_test.http
git commit -m "feat(sse): add the SSE demonstration endpoint to mimo"
```

**注意：** mimo 是独立仓库，提交前先 `git status` 确认没有带上别人的改动。

---

### Task 7: 全量验证

**Files:**
- 无代码改动，纯验证

- [ ] **Step 1: sprout 的构建、静态检查与测试**

```bash
cd sprout
env -u GOROOT go build ./...
env -u GOROOT go vet ./...
env -u GOROOT go test ./...
env -u GOROOT go test -race ./...
```

Expected: 全部成功。

- [ ] **Step 2: 不带 QUIC 的构建**

```bash
cd sprout
env -u GOROOT go build -tags noquic ./...
```

Expected: 成功。

- [ ] **Step 3: 验证模块脱离工作区也能构建**

工作区的 `go.work.sum` 会掩盖模块自身 `go.sum` 缺失的哈希行，所以必须在工作区之外验证一遍：

```bash
cd sprout
rm -rf /tmp/sprout-verify && mkdir -p /tmp/sprout-verify
git archive HEAD | tar -x -C /tmp/sprout-verify
cd /tmp/sprout-verify
env -u GOROOT GOWORK=off go build ./...
env -u GOROOT GOWORK=off go vet ./...
env -u GOROOT GOWORK=off go test ./...
```

Expected: 全部成功。**如果这里失败，说明 go.sum 缺了哈希行**，按 CLAUDE.md 的说明处理。

- [ ] **Step 4: 验证工作树里没有夹带别人的改动**

```bash
cd sprout
git status --short
git log --oneline -8
```

Expected: `git status` 里只剩下 untracked 的 `docs/`（对方的 gRPC 设计稿也在里面），没有未提交的代码改动。

---

## 附录：实现完成后的自检清单

- [ ] `sprout/api.go` 里没有出现 `Runner`、`Component`、`Lifecycle` 标识符。
- [ ] `sprout/parse_request.go` 没有被修改（既定绑定顺序保持不变）。
- [ ] `sprout/sse.go` 里没有自行拼 `context.WithValue`；流用 `ctx.setSSEStream` 登记，端点错误用既有的 `ctx.setEndpointError` 记录。
- [ ] SSE 端点的响应头是懒提交的：第一帧之前发生的错误仍然返回正确的 HTTP 状态码。
- [ ] 流已提交后的错误降级为 `event: error` 帧，JSON 不会混进 SSE 流。
- [ ] `sprout/sse.go` 的导出名字只有 `SSEEvent`、`SSEStream`、`SSEHandler`、`SSEEndpoint`，加上 `api.go` 的 `MountSSE` 与 `MimeEventStream`。

---

## 附录二：执行过程中的偏差与发现

计划写在开工之前，执行时上游代码变了，以下几处与计划原文不同。实际落地的代码以本节为准。

### 1. Task 1 从「修缺陷」变成「回归护栏」

计划写的是修 `endpoint.go` 里 `context.WithValue(ctx, ...)` 造成的上下文自引用死循环。执行时发现上游提交 `445999e`（perf: cut the allocations of the hot paths）已经把框架自带的值收进 `ctxValues` 结构体，`setEndpointError` 直接写字段，死循环的隐患不存在了。

于是 Task 1 改成一条回归测试：用一个拦截器在 handler 返回错误之后读一个**别的**上下文值，断言它不会卡住。这条测试守住的是「不要再把 `context.WithValue(ctx, ...)` 写回来」。

### 2. SSE 的流登记进 `ctxValues`，不新增上下文键

同样因为 `445999e`，`ctxValues` 成了框架自带值的唯一容身之处。所以：

- `ctxValues` 新增 `sseStream *sseStream` 字段；
- `requestContext` 新增 `setSSEStream` 与 `sseStreamOf` 两个方法；
- **不**新增 `ctxKeyTypeSSEStream` 上下文键。

端点错误也改用既有的 `ctx.setEndpointError(err)`，不再自己拼 `context.WithValue`。

### 3. 心跳测试需要线程安全的 writer

`httptest.ResponseRecorder` 不是并发安全的：心跳 goroutine 在写它，测试同时在读 `Body.String()`，`go test -race` 会报竞态。实现本身没问题（写操作由 `s.mu` 串行化），是测试的问题。

测试改用一个带锁的 `syncWriter`。同理，`Close()` 之后不再有写发生，所以「先 Close 再读 body」也是安全的。

### 4. mimo 不是 git 仓库，且 iface 包必须在 main.go 里被引用

- 工作区里只有 `basal`、`sprout`、`wql` 是 git 仓库，`mimo` **不是**。Task 6 的提交步骤不适用，mimo 的改动直接落在工作区。
- Go 的 `init()` 只在包被 import 时执行。`mimo/iface/sse` 必须在 `mimo/main.go` 里以空导入 `_ "mimo/iface/sse"` 引用，否则端点不会被注册（表现为 404）。计划原文漏了这一步。

### 5. 脱离工作区的独立构建失败：是既有问题，不是本次引入

Task 7 的 Step 3 用 `git archive` 导出到工作区外构建，会失败：

```
endpoint.go:10:2: no required module provides package github.com/wxy365/basal/json
```

原因：`sprout/endpoint.go` 早在 `7721701` 就 import 了 `github.com/wxy365/basal/json`，而 `go.mod` 钉的 `basal v0.0.0-20260511062053-f93603e08d1b` 里没有 `json` 包——该包在 basal 仓库的 `125dde9` 才加入，且**尚未推送到远端**（`origin/main` 还在 `131fc45`）。

在 `445999e`（本次开工前的基线）上独立构建，报同样的错，所以与 SSE 无关。工作区内的 `go.work` 用的是本地 basal，所以一切正常。

要修需要两件事：把 basal 的 `json` 提交推上远端，再把 sprout 的 `go.mod` 里的 basal 版本升到包含它的版本。这动的是共享的依赖状态，且 gRPC 会话同期也在改 `sprout/go.mod`，因此本次不擅自动手，留给仓库作者决定。

