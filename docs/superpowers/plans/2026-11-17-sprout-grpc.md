# sprout gRPC 集成 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 sprout 同时提供 gRPC 服务端与客户端：用户用 Go 结构体 + `pb` 标签定义消息，框架运行时合成真实 protobuf 描述符，用户不写 `.proto`、不跑 protoc，线格式仍与 protoc 产物完全一致。

**Architecture:** 新增子包 `sprout/rpc`。描述符合成层把 Go 结构体反射成 `descriptorpb.FileDescriptorProto`，经 `protodesc` 得到 `protoreflect.FileDescriptor`，消息承载用 `dynamicpb`；grpc-go 原生服务端的 `grpc.ServiceDesc` 在运行时按方法列表拼装，服务端与客户端共用同一套描述符与双向转换。客户端寻址分两条路：直连走 `passthrough:///` 直连地址，走注册中心则由自定义 `resolver.Builder` 把注册中心的实例列表推给 grpc-go，由 grpc-go 原生负责重连与负载均衡。

**Tech Stack:** Go 1.26、`google.golang.org/grpc v1.85.0`、`google.golang.org/protobuf v1.36.12`、`github.com/wxy365/basal`（errs / log / cfg）。

**Spec:** `docs/superpowers/specs/2026-11-17-sprout-grpc-design.md`

---

## Global Constraints

以下约束对每个任务都生效，不再逐条重复：

- **构建命令**：`GOROOT` 指向 Go 1.25 SDK，PATH 里的 `go` 是 1.27.3，两者不可混用。所有 go 命令一律加 `env -u GOROOT` 前缀。测试用 `cd sprout && env -u GOROOT go test ./...`。
- **依赖版本固定**：`google.golang.org/grpc v1.85.0`（要求 go 1.26.0，与 sprout 的 `go 1.26.0` 一致）、`google.golang.org/protobuf v1.36.12`。不升级到更高版本。
- **不引入第二套日志库**：日志一律用 `github.com/wxy365/basal/log`（`log.Info` / `log.Warn` / `log.ErrorErrF` 等）。不用 zap / logrus / 标准库 log。
- **错误一律用 `github.com/wxy365/basal/errs`**：`errs.New` / `errs.Wrap` / `errs.I18nNew`，业务错误用 `*errs.Err` 并带 `Status`。
- **核心包不 import `sprout/rpc`**：`sprout/rpc` import 核心包 `sprout`，反向禁止，避免循环依赖。跨包复用一律通过核心包 `api.go` 的导出别名。
- **提交纪律**：不使用 `git add .`，只 `git add` 本任务实际改动的文件路径；提交前先 `git status` 确认暂存区里没有别人的文件。
- **字段号标签**：消息结构体的**每个**字段都必须带 `pb:"<正整数>"` 标签；缺失、非法、重复一律在注册服务时**报错**，不静默忽略。
- **嵌套结构体提升为顶层 message**：Go 的嵌套结构体不生成 protobuf 的嵌套 message，而是提升为同包下的顶层 message（`demo.Address`）。protobuf 线格式只取决于字段号与字段类型，因此不影响互操作性。`map` 的 entry 类型是 protobuf 的硬性要求，必须嵌套在所属消息内，命名为 `<字段名>Entry`。
- **proto3 optional**：指针标量（`*int32` 等）生成显式存在性字段，配套合成 oneof，名称为 `_<字段名>`。指针指向结构体时按普通 message 处理（message 本身即有存在性）。
- **`time.Time` 映射为 `google.protobuf.Timestamp`**：文件需带 `google/protobuf/timestamp.proto` 依赖，用 `protoregistry.GlobalFiles` 解析。
- **并发会话**：另一会话正在实现 SSE 端点，会改 `sprout/app.go`、`sprout/api.go`、`sprout/endpoint.go`、`sprout/context.go`，并新增 `sprout/sse.go`、`sprout/sse_test.go`。本计划的 Task 4 与 Task 6 会碰 `app.go` / `api.go`，**动手前必须重新读取这两个文件的当前内容**，用精确字符串替换，不要整文件重写。

## 文件结构

| 文件 | 职责 |
|---|---|
| `sprout/rpc/descriptor.go` | Go 结构体 → protobuf 描述符的合成；字段号解析、类型映射、命名规则 |
| `sprout/rpc/convert.go` | Go 结构体 ↔ `dynamicpb` 消息双向转换 |
| `sprout/rpc/errors.go` | `errs.Err` ↔ gRPC status 双向映射 |
| `sprout/rpc/service.go` | `Service` / `Method` / `Unary` / `Context` 定义，`grpc.ServiceDesc` 拼装 |
| `sprout/rpc/stream.go` | `ServerStream[I,O]` / `ClientStream[I,O]` 与流式方法 |
| `sprout/rpc/server.go` | `rpc.Server`：gRPC 服务端实例、配置、生命周期、`Mount` |
| `sprout/rpc/client.go` | `Client`：连接、选项、`Doer` / `StreamDoer` |
| `sprout/rpc/resolver.go` | `Registry` 接口与 `resolver.Builder` 实现 |
| `sprout/rpc/nacos.go` | Nacos 注册中心适配（开放 HTTP API，零新依赖） |
| `sprout/rpc/interceptor.go` | gRPC 拦截器：panic 恢复、熔断、限流 |
| `sprout/rpc/api.go` | `sprout/rpc` 包的公共 API 汇总（类型别名与薄再导出） |

核心包改动：

| 文件 | 改动 |
|---|---|
| `sprout/server.go` | 抽出包级 `buildInputEntityValidateFuncs`，原方法保留为薄封装 |
| `sprout/interceptor.go` | 抽出 `circuitBreakerGuard` / `rateLimiterGuard`，HTTP 拦截器改用之，行为不变 |
| `sprout/api.go` | 导出 `Runner`、`DefaultValidators`、`BuildInputEntityValidateFuncs`、两个守卫 |
| `sprout/app.go` | 新增 `Runner` 接口与 `app.Runners` 字段，启动顺序调整 |

---

### Task 1: 消息描述符合成

**Files:**
- Create: `sprout/rpc/descriptor.go`
- Test: `sprout/rpc/descriptor_test.go`
- Modify: `sprout/go.mod`（新增 grpc / protobuf 依赖）、`sprout/go.sum`

**Interfaces:**
- Consumes: 无（首个任务）
- Produces:
  - `const tagName = "pb"`
  - `func parseTag(tag string) (int32, error)`
  - `func jsonName(name string) string`
  - `func buildFileDescriptor(pkg, fileName string, roots []reflect.Type) (protoreflect.FileDescriptor, map[string]protoreflect.MessageDescriptor, error)`
  - `func collectTypes(roots []reflect.Type) ([]reflect.Type, error)`

- [ ] **Step 1: 加依赖并写下失败的测试**

在 `sprout/go.mod` 增加：

```
require (
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
)
```

然后 `cd sprout && env -u GOROOT go mod tidy`。

新建 `sprout/rpc/descriptor_test.go`：

```go
package rpc

import (
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type descAddress struct {
	City string `pb:"1"`
	Zip  string `pb:"2"`
}

type descHelloReq struct {
	Name     string            `pb:"1"`
	Age      int32             `pb:"2"`
	Tags     []string          `pb:"3"`
	Addr     *descAddress      `pb:"4"`
	Meta     map[string]string `pb:"5"`
	Payload  []byte            `pb:"6"`
	Optional *int32            `pb:"7"`
}

func TestBuildFileDescriptorFieldTypes(t *testing.T) {
	_, msgs, err := buildFileDescriptor("demo", "demo/hello.proto", []reflect.Type{reflect.TypeOf(descHelloReq{})})
	if err != nil {
		t.Fatalf("buildFileDescriptor: %v", err)
	}
	md := msgs["demo.descHelloReq"]
	if md == nil {
		t.Fatalf("message demo.descHelloReq not found")
	}
	want := []struct {
		num  protoreflect.FieldNumber
		kind protoreflect.Kind
	}{
		{1, protoreflect.StringKind},
		{2, protoreflect.Int32Kind},
		{3, protoreflect.StringKind},
		{4, protoreflect.MessageKind},
		{5, protoreflect.MessageKind},
		{6, protoreflect.BytesKind},
		{7, protoreflect.Int32Kind},
	}
	for _, w := range want {
		fd := md.Fields().ByNumber(w.num)
		if fd == nil {
			t.Fatalf("field %d not found", w.num)
		}
		if fd.Kind() != w.kind {
			t.Errorf("field %d kind = %s, want %s", w.num, fd.Kind(), w.kind)
		}
	}
	// 嵌套结构体提升为顶层 message
	if msgs["demo.descAddress"] == nil {
		t.Errorf("the nested struct should be promoted to a top level message")
	}
	// map 字段是 repeated message
	meta := md.Fields().ByNumber(5)
	if !meta.IsMap() {
		t.Errorf("field 5 should be a map")
	}
	if !meta.IsList() {
		t.Errorf("a map field is a repeated field")
	}
	// 数组字段是 repeated
	if !md.Fields().ByNumber(3).IsList() {
		t.Errorf("field 3 should be repeated")
	}
	// 字段名与 json 名
	if got := md.Fields().ByNumber(1).Name(); got != "Name" {
		t.Errorf("field name = %q, want %q", got, "Name")
	}
	if got := md.Fields().ByNumber(1).JSONName(); got != "name" {
		t.Errorf("json name = %q, want %q", got, "name")
	}
}

func TestBuildFileDescriptorProto3Optional(t *testing.T) {
	_, msgs, err := buildFileDescriptor("demo", "demo/hello.proto", []reflect.Type{reflect.TypeOf(descHelloReq{})})
	if err != nil {
		t.Fatalf("buildFileDescriptor: %v", err)
	}
	md := msgs["demo.descHelloReq"]
	fd := md.Fields().ByNumber(7)
	if !fd.HasPresence() {
		t.Errorf("a pointer scalar should have explicit presence")
	}
	if fd.ContainingOneof() == nil {
		t.Fatalf("a proto3 optional field should belong to a synthetic oneof")
	}
	if got := string(fd.ContainingOneof().Name()); got != "_Optional" {
		t.Errorf("synthetic oneof name = %q, want %q", got, "_Optional")
	}
}

func TestBuildFileDescriptorTimestamp(t *testing.T) {
	type withTime struct {
		At time.Time `pb:"1"`
	}
	_, msgs, err := buildFileDescriptor("demo", "demo/t.proto", []reflect.Type{reflect.TypeOf(withTime{})})
	if err != nil {
		t.Fatalf("buildFileDescriptor: %v", err)
	}
	md := msgs["demo.withTime"]
	fd := md.Fields().ByNumber(1)
	if got := string(fd.Message().FullName()); got != "google.protobuf.Timestamp" {
		t.Errorf("time.Time should map to google.protobuf.Timestamp, got %q", got)
	}
}

func TestBuildFileDescriptorRejectsBadFieldNumber(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{"missing tag", reflect.TypeOf(struct {
			Name string
		}{})},
		{"zero field number", reflect.TypeOf(struct {
			Name string `pb:"0"`
		}{})},
		{"not a number", reflect.TypeOf(struct {
			Name string `pb:"abc"`
		}{})},
		{"duplicated", reflect.TypeOf(struct {
			A string `pb:"1"`
			B string `pb:"1"`
		}{})},
	}
	for _, c := range cases {
		if _, _, err := buildFileDescriptor("demo", "demo/bad.proto", []reflect.Type{c.typ}); err == nil {
			t.Errorf("%s: expected an error, got none", c.name)
		}
	}
}

// TestWireCompatWithProtocGenerated 证明合成出来的描述符与 protoc 生成的类型
// 线格式一致：protoc 产出的字节能被合成描述符解析，且重新序列化后逐字节相同。
func TestWireCompatWithProtocGenerated(t *testing.T) {
	cases := []struct {
		name string
		gen  proto.Message
		typ  reflect.Type
	}{
		{"string", wrapperspb.String("hi"), reflect.TypeOf(struct {
			Value string `pb:"1"`
		}{})},
		{"bytes", wrapperspb.Bytes([]byte("abc")), reflect.TypeOf(struct {
			Value []byte `pb:"1"`
		}{})},
		{"int32", wrapperspb.Int32(42), reflect.TypeOf(struct {
			Value int32 `pb:"1"`
		}{})},
		{"int64", wrapperspb.Int64(-9), reflect.TypeOf(struct {
			Value int64 `pb:"1"`
		}{})},
		{"bool", wrapperspb.Bool(true), reflect.TypeOf(struct {
			Value bool `pb:"1"`
		}{})},
		{"double", wrapperspb.Double(3.5), reflect.TypeOf(struct {
			Value float64 `pb:"1"`
		}{})},
	}
	for _, c := range cases {
		_, msgs, err := buildFileDescriptor("compat", "compat/c.proto", []reflect.Type{c.typ})
		if err != nil {
			t.Fatalf("%s: buildFileDescriptor: %v", c.name, err)
		}
		md := msgs["compat."+c.typ.Name()]
		if md == nil {
			t.Fatalf("%s: message compat.%s not found", c.name, c.typ.Name())
		}
		want, err := proto.Marshal(c.gen)
		if err != nil {
			t.Fatalf("%s: marshal generated: %v", c.name, err)
		}
		dm := newDynamicMessage(md)
		if err := proto.Unmarshal(want, dm); err != nil {
			t.Fatalf("%s: unmarshal into the synthesized descriptor: %v", c.name, err)
		}
		got, err := proto.Marshal(dm)
		if err != nil {
			t.Fatalf("%s: marshal dynamic: %v", c.name, err)
		}
		if string(want) != string(got) {
			t.Errorf("%s: wire mismatch\n generated: %x\n dynamic:   %x", c.name, want, got)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— `buildFileDescriptor` 未定义（编译错误）。

- [ ] **Step 3: 实现描述符合成**

新建 `sprout/rpc/descriptor.go`：

```go
package rpc

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"time"
	"unicode"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// tagName 是消息结构体上声明 protobuf 字段号的结构体标签。
const tagName = "pb"

// timestampTypeName 是 time.Time 对应的 well-known 类型全名。
const timestampTypeName = "google.protobuf.Timestamp"

// timestampProtoFile 是 time.Time 对应的 well-known 类型文件路径。
const timestampProtoFile = "google/protobuf/timestamp.proto"

// timeType 是 time.Time 的反射类型，用于识别时间字段。
var timeType = reflect.TypeOf(time.Time{})

// scalarTypes 把 Go 标量类型映射到 protobuf 标量类型。
var scalarTypes = map[reflect.Kind]descriptorpb.FieldDescriptorProto_Type{
	reflect.String:  descriptorpb.FieldDescriptorProto_TYPE_STRING,
	reflect.Bool:    descriptorpb.FieldDescriptorProto_TYPE_BOOL,
	reflect.Int32:   descriptorpb.FieldDescriptorProto_TYPE_INT32,
	reflect.Int64:   descriptorpb.FieldDescriptorProto_TYPE_INT64,
	reflect.Uint32:  descriptorpb.FieldDescriptorProto_TYPE_UINT32,
	reflect.Uint64:  descriptorpb.FieldDescriptorProto_TYPE_UINT64,
	reflect.Float32: descriptorpb.FieldDescriptorProto_TYPE_FLOAT,
	reflect.Float64: descriptorpb.FieldDescriptorProto_TYPE_DOUBLE,
}

// parseTag 解析 pb 标签，返回字段号。标签缺失或非法时返回错误。
func parseTag(tag string) (int32, error) {
	if tag == "" {
		return 0, fmt.Errorf("the %q tag is required on every field", tagName)
	}
	num, err := strconv.Atoi(tag)
	if err != nil {
		return 0, fmt.Errorf("the %q tag %q is not a valid field number", tagName, tag)
	}
	if num <= 0 {
		return 0, fmt.Errorf("the %q tag %q must be a positive field number", tagName, tag)
	}
	return int32(num), nil
}

// jsonName 返回字段的 JSON 名，即字段名的小驼峰形式，与 protoc 的默认规则一致。
func jsonName(name string) string {
	var b []rune
	upperNext := false
	for i, r := range name {
		if r == '_' {
			upperNext = true
			continue
		}
		if i == 0 {
			b = append(b, unicode.ToLower(r))
			continue
		}
		if upperNext {
			b = append(b, unicode.ToUpper(r))
			upperNext = false
			continue
		}
		b = append(b, r)
	}
	return string(b)
}

// derefType 去掉指针，返回底层类型。
func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// collectTypes 收集给定类型可达的全部消息类型，结果按类型名排序以保证确定性。
func collectTypes(roots []reflect.Type) ([]reflect.Type, error) {
	seen := map[reflect.Type]bool{}
	var list []reflect.Type
	var walk func(t reflect.Type) error
	walk = func(t reflect.Type) error {
		if seen[t] {
			return nil
		}
		seen[t] = true
		list = append(list, t)
		for i := 0; i < t.NumField(); i++ {
			ft := derefType(t.Field(i).Type)
			switch {
			case ft == timeType:
				continue
			case ft.Kind() == reflect.Map:
				v := derefType(ft.Elem())
				if v.Kind() == reflect.Struct {
					if err := walk(v); err != nil {
						return err
					}
				}
			case ft.Kind() == reflect.Slice && ft.Elem().Kind() != reflect.Uint8:
				e := derefType(ft.Elem())
				if e.Kind() == reflect.Struct {
					if err := walk(e); err != nil {
						return err
					}
				}
			case ft.Kind() == reflect.Struct:
				if err := walk(ft); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, r := range roots {
		if r.Kind() != reflect.Struct {
			return nil, fmt.Errorf("the message type %s must be a struct", r)
		}
		if err := walk(r); err != nil {
			return nil, err
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
	return list, nil
}

// descriptorBuilder 把 Go 结构体类型合成为一个 protobuf 文件描述符。
type descriptorBuilder struct {
	pkg      string
	messages []*descriptorpb.DescriptorProto
	// needsTimestamp 记录文件是否引用了 Timestamp 这个 well-known 类型。
	needsTimestamp bool
}

// buildFileDescriptor 把给定的 Go 结构体类型合成为一个 protobuf 文件描述符。
// 返回文件描述符，以及「消息全名 → 消息描述符」的映射。
func buildFileDescriptor(pkg, fileName string, roots []reflect.Type) (protoreflect.FileDescriptor, map[string]protoreflect.MessageDescriptor, error) {
	types, err := collectTypes(roots)
	if err != nil {
		return nil, nil, err
	}
	b := &descriptorBuilder{pkg: pkg}
	for _, t := range types {
		dp, err := b.buildMessage(t)
		if err != nil {
			return nil, nil, err
		}
		b.messages = append(b.messages, dp)
	}
	fdp := &descriptorpb.FileDescriptorProto{
		Name:        proto.String(fileName),
		Package:     proto.String(pkg),
		Syntax:      proto.String("proto3"),
		MessageType: b.messages,
	}
	if b.needsTimestamp {
		fdp.Dependency = append(fdp.Dependency, timestampProtoFile)
	}
	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot build the file descriptor %s: %w", fileName, err)
	}
	msgs := make(map[string]protoreflect.MessageDescriptor, fd.Messages().Len())
	for i := 0; i < fd.Messages().Len(); i++ {
		md := fd.Messages().Get(i)
		msgs[string(md.FullName())] = md
	}
	return fd, msgs, nil
}

// buildMessage 把一个 Go 结构体类型合成为一个 protobuf 消息描述。
func (b *descriptorBuilder) buildMessage(t reflect.Type) (*descriptorpb.DescriptorProto, error) {
	dp := &descriptorpb.DescriptorProto{Name: proto.String(t.Name())}
	seen := map[int32]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		num, err := parseTag(f.Tag.Get(tagName))
		if err != nil {
			return nil, fmt.Errorf("message %s: field %s: %w", t.Name(), f.Name, err)
		}
		if seen[num] {
			return nil, fmt.Errorf("message %s: field %s: duplicated field number %d", t.Name(), f.Name, num)
		}
		seen[num] = true
		fdp, err := b.buildField(dp, t, f, num)
		if err != nil {
			return nil, fmt.Errorf("message %s: field %s: %w", t.Name(), f.Name, err)
		}
		dp.Field = append(dp.Field, fdp)
	}
	return dp, nil
}

// buildField 把一个结构体字段合成为一个 protobuf 字段描述。
func (b *descriptorBuilder) buildField(parent *descriptorpb.DescriptorProto, owner reflect.Type, f reflect.StructField, num int32) (*descriptorpb.FieldDescriptorProto, error) {
	fdp := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(f.Name),
		JsonName: proto.String(jsonName(f.Name)),
		Number:   proto.Int32(num),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
	ft := f.Type
	pointer := ft.Kind() == reflect.Pointer
	ft = derefType(ft)

	switch {
	case ft == timeType:
		b.needsTimestamp = true
		fdp.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		fdp.TypeName = proto.String("." + timestampTypeName)
	case ft.Kind() == reflect.Map:
		if err := b.buildMapField(parent, owner, fdp, ft); err != nil {
			return nil, err
		}
	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Uint8:
		fdp.Type = descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum()
	case ft.Kind() == reflect.Slice:
		fdp.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		elem := derefType(ft.Elem())
		if st, ok := scalarTypes[elem.Kind()]; ok {
			fdp.Type = st.Enum()
		} else if elem.Kind() == reflect.Struct {
			fdp.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
			fdp.TypeName = proto.String("." + b.pkg + "." + elem.Name())
		} else {
			return nil, fmt.Errorf("the element type %s of the slice is not supported", elem)
		}
	case ft.Kind() == reflect.Struct:
		fdp.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		fdp.TypeName = proto.String("." + b.pkg + "." + ft.Name())
	default:
		st, ok := scalarTypes[ft.Kind()]
		if !ok {
			return nil, fmt.Errorf("the field type %s is not supported", ft)
		}
		fdp.Type = st.Enum()
	}

	// 指针标量生成 proto3 optional：需要配套一个合成 oneof。
	if pointer && fdp.GetType() != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
		parent.OneofDecl = append(parent.OneofDecl, &descriptorpb.OneofDescriptorProto{
			Name: proto.String("_" + f.Name),
		})
		fdp.Proto3Optional = proto.Bool(true)
		fdp.OneofIndex = proto.Int32(int32(len(parent.OneofDecl) - 1))
	}
	return fdp, nil
}

// buildMapField 为 map 字段合成 map entry 嵌套类型。
func (b *descriptorBuilder) buildMapField(parent *descriptorpb.DescriptorProto, ownerName, fieldName string, fdp *descriptorpb.FieldDescriptorProto, ft reflect.Type) error {
	if ft.Key().Kind() != reflect.String {
		return fmt.Errorf("the map key type %s is not supported, only string keys are supported", ft.Key())
	}
	entryName := fieldName + "Entry"
	valueField := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String("value"),
		JsonName: proto.String("value"),
		Number:   proto.Int32(2),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
	valueType := derefType(ft.Elem())
	switch {
	case valueType == timeType:
		b.needsTimestamp = true
		valueField.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		valueField.TypeName = proto.String("." + timestampTypeName)
	case valueType.Kind() == reflect.Struct:
		valueField.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		valueField.TypeName = proto.String("." + b.pkg + "." + valueType.Name())
	default:
		st, ok := scalarTypes[valueType.Kind()]
		if !ok {
			return fmt.Errorf("the map value type %s is not supported", valueType)
		}
		valueField.Type = st.Enum()
	}
	entry := &descriptorpb.DescriptorProto{
		Name: proto.String(entryName),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     proto.String("key"),
				JsonName: proto.String("key"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			},
			valueField,
		},
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
	}
	parent.NestedType = append(parent.NestedType, entry)
	fdp.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	fdp.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
	fdp.TypeName = proto.String("." + b.pkg + "." + ownerName + "." + entryName)
	return nil
}
```

对应的调用点改为：

```go
	case ft.Kind() == reflect.Map:
		if err := b.buildMapField(parent, owner.Name(), f.Name, fdp, ft); err != nil {
			return nil, err
		}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: PASS —— 全部用例通过。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add rpc/descriptor.go rpc/descriptor_test.go go.mod go.sum
git commit -m "feat(rpc): synthesize the protobuf descriptors from the go structs"
```

---

### Task 2: 结构体与动态消息的双向转换

**Files:**
- Create: `sprout/rpc/convert.go`
- Test: `sprout/rpc/convert_test.go`

**Interfaces:**
- Consumes: Task 1 的 `parseTag`、`tagName`、`timestampTypeName`
- Produces:
  - `func newDynamicMessage(md protoreflect.MessageDescriptor) *dynamicpb.Message`
  - `func toMessage(v reflect.Value, msg protoreflect.Message) error`
  - `func fromMessage(msg protoreflect.Message, out reflect.Value) error`

- [ ] **Step 1: 写失败的测试**

新建 `sprout/rpc/convert_test.go`：

```go
package rpc

import (
	"reflect"
	"testing"
	"time"
)

func TestToAndFromMessage(t *testing.T) {
	in := &descHelloReq{
		Name:     "wxy",
		Age:      30,
		Tags:     []string{"a", "b"},
		Addr:     &descAddress{City: "Shanghai", Zip: "200000"},
		Meta:     map[string]string{"k": "v"},
		Payload:  []byte("bin"),
		Optional: nil,
	}
	_, msgs, err := buildFileDescriptor("demo", "demo/hello.proto", []reflect.Type{reflect.TypeOf(descHelloReq{})})
	if err != nil {
		t.Fatalf("buildFileDescriptor: %v", err)
	}
	md := msgs["demo.descHelloReq"]
	dm := newDynamicMessage(md)
	if err := toMessage(reflect.ValueOf(in).Elem(), dm); err != nil {
		t.Fatalf("toMessage: %v", err)
	}
	var out descHelloReq
	if err := fromMessage(dm, reflect.ValueOf(&out).Elem()); err != nil {
		t.Fatalf("fromMessage: %v", err)
	}
	if out.Name != in.Name || out.Age != in.Age {
		t.Errorf("scalars not preserved: %+v", out)
	}
	if len(out.Tags) != 2 || out.Tags[0] != "a" {
		t.Errorf("repeated not preserved: %v", out.Tags)
	}
	if out.Addr == nil || out.Addr.City != "Shanghai" {
		t.Errorf("nested message not preserved: %+v", out.Addr)
	}
	if out.Meta["k"] != "v" {
		t.Errorf("map not preserved: %v", out.Meta)
	}
	if string(out.Payload) != "bin" {
		t.Errorf("bytes not preserved: %v", out.Payload)
	}
}

func TestToAndFromMessageTimestamp(t *testing.T) {
	type withTime struct {
		At time.Time `pb:"1"`
	}
	at := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	_, msgs, err := buildFileDescriptor("demo", "demo/t.proto", []reflect.Type{reflect.TypeOf(withTime{})})
	if err != nil {
		t.Fatalf("buildFileDescriptor: %v", err)
	}
	md := msgs["demo.withTime"]
	dm := newDynamicMessage(md)
	if err := toMessage(reflect.ValueOf(&withTime{At: at}).Elem(), dm); err != nil {
		t.Fatalf("toMessage: %v", err)
	}
	var out withTime
	if err := fromMessage(dm, reflect.ValueOf(&out).Elem()); err != nil {
		t.Fatalf("fromMessage: %v", err)
	}
	if !out.At.Equal(at) {
		t.Errorf("timestamp not preserved: %v want %v", out.At, at)
	}
}

func TestFromMessageSkipsAbsentFields(t *testing.T) {
	_, msgs, err := buildFileDescriptor("demo", "demo/hello.proto", []reflect.Type{reflect.TypeOf(descHelloReq{})})
	if err != nil {
		t.Fatalf("buildFileDescriptor: %v", err)
	}
	dm := newDynamicMessage(msgs["demo.descHelloReq"])
	out := descHelloReq{Name: "keep"}
	if err := fromMessage(dm, reflect.ValueOf(&out).Elem()); err != nil {
		t.Fatalf("fromMessage: %v", err)
	}
	if out.Name != "keep" {
		t.Errorf("an absent field must not overwrite the existing value, got %q", out.Name)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— `toMessage` / `fromMessage` / `newDynamicMessage` 未定义。

- [ ] **Step 3: 实现转换**

新建 `sprout/rpc/convert.go`：

```go
package rpc

import (
	"fmt"
	"reflect"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// newDynamicMessage 为给定的消息描述符创建一个空的动态消息。
func newDynamicMessage(md protoreflect.MessageDescriptor) *dynamicpb.Message {
	return dynamicpb.NewMessage(md)
}

// toMessage 把 Go 结构体的值写入 protobuf 动态消息。
func toMessage(v reflect.Value, msg protoreflect.Message) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		num, err := parseTag(f.Tag.Get(tagName))
		if err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
		fd := msg.Descriptor().Fields().ByNumber(protoreflect.FieldNumber(num))
		if fd == nil {
			return fmt.Errorf("field %s is not in the descriptor", f.Name)
		}
		fv := v.Field(i)
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				continue
			}
			fv = fv.Elem()
		}
		if err := setField(fd, fv, msg); err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
	}
	return nil
}

// setField 把一个 Go 值写入消息的对应字段。
func setField(fd protoreflect.FieldDescriptor, fv reflect.Value, msg protoreflect.Message) error {
	switch {
	case fd.IsMap():
		if fv.IsNil() {
			return nil
		}
		mv := msg.Mutable(fd).Map()
		iter := fv.MapRange()
		for iter.Next() {
			k := reflect.ValueOf(iter.Key().String()).Convert(fv.Type().Key())
			vv, err := singleValue(fd.MapValue(), iter.Value())
			if err != nil {
				return err
			}
			mv.Set(protoreflect.ValueOfString(k.String()).MapKey(), vv)
		}
		return nil
	case fd.IsList():
		lv := msg.Mutable(fd).List()
		for i := 0; i < fv.Len(); i++ {
			ev := fv.Index(i)
			if ev.Kind() == reflect.Pointer {
				if ev.IsNil() {
					continue
				}
				ev = ev.Elem()
			}
			pv, err := singleValue(fd, ev)
			if err != nil {
				return err
			}
			lv.Append(pv)
		}
		return nil
	default:
		pv, err := singleValue(fd, fv)
		if err != nil {
			return err
		}
		msg.Set(fd, pv)
		return nil
	}
}

// singleValue 把一个 Go 值转成 protobuf 的字段值。
func singleValue(fd protoreflect.FieldDescriptor, v reflect.Value) (protoreflect.Value, error) {
	switch fd.Kind() {
	case protoreflect.MessageKind:
		if string(fd.Message().FullName()) == timestampTypeName {
			t, ok := v.Interface().(time.Time)
			if !ok {
				return protoreflect.Value{}, fmt.Errorf("the timestamp field expects a time.Time")
			}
			ts := timestamppb.New(t)
			sub := newDynamicMessage(fd.Message())
			sub.Set(fd.Message().Fields().ByNumber(1), protoreflect.ValueOfInt64(ts.Seconds))
			sub.Set(fd.Message().Fields().ByNumber(2), protoreflect.ValueOfInt32(ts.Nanos))
			return protoreflect.ValueOfMessage(sub), nil
		}
		sub := newDynamicMessage(fd.Message())
		if err := toMessage(v, sub); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfMessage(sub), nil
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(v.String()), nil
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(v.Bool()), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(int32(v.Int())), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(v.Int()), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(uint32(v.Uint())), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(v.Uint()), nil
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(float32(v.Float())), nil
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(v.Float()), nil
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes(v.Bytes()), nil
	}
	return protoreflect.Value{}, fmt.Errorf("the field kind %s is not supported", fd.Kind())
}

// fromMessage 把 protobuf 动态消息读入 Go 结构体。
func fromMessage(msg protoreflect.Message, out reflect.Value) error {
	t := out.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		num, err := parseTag(f.Tag.Get(tagName))
		if err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
		fd := msg.Descriptor().Fields().ByNumber(protoreflect.FieldNumber(num))
		if fd == nil {
			continue
		}
		if !msg.Has(fd) {
			continue
		}
		if err := loadField(fd, msg.Get(fd), out.Field(i)); err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
	}
	return nil
}

// loadField 把一个 protobuf 字段值读入 Go 的对应字段。
func loadField(fd protoreflect.FieldDescriptor, pv protoreflect.Value, fv reflect.Value) error {
	switch {
	case fd.IsMap():
		if fv.IsNil() {
			fv.Set(reflect.MakeMap(fv.Type()))
		}
		pv.Map().Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
			fv.SetMapIndex(
				reflect.ValueOf(k.String()).Convert(fv.Type().Key()),
				reflect.ValueOf(v.Interface()).Convert(fv.Type().Elem()),
			)
			return true
		})
		return nil
	case fd.IsList():
		lv := pv.List()
		slice := reflect.MakeSlice(fv.Type(), 0, lv.Len())
		for i := 0; i < lv.Len(); i++ {
			elem := reflect.New(fv.Type().Elem()).Elem()
			if err := loadSingle(fd, lv.Get(i), elem); err != nil {
				return err
			}
			slice = reflect.Append(slice, elem)
		}
		fv.Set(slice)
		return nil
	default:
		return loadSingle(fd, pv, fv)
	}
}

// loadSingle 把一个 protobuf 字段值读入一个 Go 值。
func loadSingle(fd protoreflect.FieldDescriptor, pv protoreflect.Value, fv reflect.Value) error {
	if fd.Kind() == protoreflect.MessageKind {
		if string(fd.Message().FullName()) == timestampTypeName {
			fields := fd.Message().Fields()
			sec := pv.Message().Get(fields.ByNumber(1)).Int()
			nanos := pv.Message().Get(fields.ByNumber(2)).Int()
			t := time.Unix(sec, nanos).UTC()
			if fv.Kind() == reflect.Pointer {
				p := reflect.New(fv.Type().Elem())
				p.Elem().Set(reflect.ValueOf(t))
				fv.Set(p)
				return nil
			}
			fv.Set(reflect.ValueOf(t))
			return nil
		}
		target := fv
		if fv.Kind() == reflect.Pointer {
			target = reflect.New(fv.Type().Elem()).Elem()
		}
		if err := fromMessage(pv.Message(), target); err != nil {
			return err
		}
		if fv.Kind() == reflect.Pointer {
			p := reflect.New(fv.Type().Elem())
			p.Elem().Set(target)
			fv.Set(p)
			return nil
		}
		fv.Set(target)
		return nil
	}
	fv.Set(reflect.ValueOf(pv.Interface()).Convert(fv.Type()))
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add rpc/convert.go rpc/convert_test.go
git commit -m "feat(rpc): convert between the go structs and the dynamic messages"
```

---

### Task 3: 业务错误与 gRPC 状态的映射

**Files:**
- Create: `sprout/rpc/errors.go`
- Test: `sprout/rpc/errors_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `func toStatus(err error) error`
  - `func fromStatus(err error) error`
  - `func httpStatusToGRPCCode(status int) codes.Code`
  - `func grpcCodeToHTTPStatus(c codes.Code) int`

- [ ] **Step 1: 写失败的测试**

新建 `sprout/rpc/errors_test.go`：

```go
package rpc

import (
	"errors"
	"net/http"
	"testing"

	"github.com/wxy365/basal/errs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatusKeepsTheCodeAndTheMessage(t *testing.T) {
	err := errs.New("name is required").WithCode("guest.name.required").WithStatus(http.StatusBadRequest)
	st, ok := status.FromError(toStatus(err))
	if !ok {
		t.Fatalf("the error is not a gRPC status")
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %s, want %s", st.Code(), codes.InvalidArgument)
	}
	if st.Message() != "name is required" {
		t.Errorf("message = %q", st.Message())
	}
}

func TestFromStatusRestoresTheBusinessError(t *testing.T) {
	err := errs.New("name is required").WithCode("guest.name.required").WithStatus(http.StatusBadRequest)
	restored := fromStatus(toStatus(err))
	var e *errs.Err
	if !errors.As(restored, &e) {
		t.Fatalf("the restored error is not an *errs.Err: %v", restored)
	}
	if e.Code != "guest.name.required" {
		t.Errorf("code = %q", e.Code)
	}
	if e.Message != "name is required" {
		t.Errorf("message = %q", e.Message)
	}
	if e.Status != http.StatusBadRequest {
		t.Errorf("status = %d", e.Status)
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		http int
		code codes.Code
	}{
		{http.StatusBadRequest, codes.InvalidArgument},
		{http.StatusUnauthorized, codes.Unauthenticated},
		{http.StatusForbidden, codes.PermissionDenied},
		{http.StatusNotFound, codes.NotFound},
		{http.StatusConflict, codes.Aborted},
		{http.StatusTooManyRequests, codes.ResourceExhausted},
		{http.StatusInternalServerError, codes.Internal},
		{http.StatusServiceUnavailable, codes.Unavailable},
		{http.StatusGatewayTimeout, codes.DeadlineExceeded},
	}
	for _, c := range cases {
		if got := httpStatusToGRPCCode(c.http); got != c.code {
			t.Errorf("http %d -> %s, want %s", c.http, got, c.code)
		}
		if got := grpcCodeToHTTPStatus(c.code); got != c.http {
			t.Errorf("%s -> %d, want %d", c.code, got, c.http)
		}
	}
}

func TestToStatusOnAPlainError(t *testing.T) {
	st, ok := status.FromError(toStatus(errors.New("boom")))
	if !ok {
		t.Fatalf("the error is not a gRPC status")
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %s, want %s", st.Code(), codes.Internal)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— `toStatus` 未定义。

- [ ] **Step 3: 实现映射**

新建 `sprout/rpc/errors.go`：

```go
package rpc

import (
	"errors"
	"net/http"

	"github.com/wxy365/basal/errs"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errorInfoDomain 是承载业务错误码的 ErrorInfo 域。
const errorInfoDomain = "sprout"

// httpStatusToGRPCCode 把 HTTP 状态码映射为 gRPC 状态码。
func httpStatusToGRPCCode(httpStatus int) codes.Code {
	switch httpStatus {
	case http.StatusBadRequest:
		return codes.InvalidArgument
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.NotFound
	case http.StatusConflict:
		return codes.Aborted
	case http.StatusTooManyRequests:
		return codes.ResourceExhausted
	case 499:
		return codes.Canceled
	case http.StatusServiceUnavailable:
		return codes.Unavailable
	case http.StatusGatewayTimeout:
		return codes.DeadlineExceeded
	case http.StatusInternalServerError:
		return codes.Internal
	}
	return codes.Unknown
}

// grpcCodeToHTTPStatus 把 gRPC 状态码映射回 HTTP 状态码。
func grpcCodeToHTTPStatus(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.Aborted:
		return http.StatusConflict
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.Canceled:
		return 499
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.Internal:
		return http.StatusInternalServerError
	}
	return http.StatusInternalServerError
}

// toStatus 把业务错误转换为 gRPC 状态。业务错误码通过 ErrorInfo 承载。
func toStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	var e *errs.Err
	if !errors.As(err, &e) {
		return status.Error(codes.Internal, err.Error())
	}
	code := httpStatusToGRPCCode(e.Status)
	st := status.New(code, e.Message)
	if e.Code == "" {
		return st.Err()
	}
	withDetails, dErr := st.WithDetails(&errdetails.ErrorInfo{
		Reason:   e.Code,
		Domain:   errorInfoDomain,
		Metadata: map[string]string{"status": strconv.Itoa(e.Status)},
	})
	if dErr != nil {
		return st.Err()
	}
	return withDetails.Err()
}

// fromStatus 把 gRPC 状态还原为业务错误。
func fromStatus(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	e := &errs.Err{
		Message: st.Message(),
		Status:  grpcCodeToHTTPStatus(st.Code()),
	}
	for _, d := range st.Details() {
		info, ok := d.(*errdetails.ErrorInfo)
		if !ok {
			continue
		}
		e.Code = info.Reason
		if v, ok := info.Metadata["status"]; ok {
			if n, convErr := strconv.Atoi(v); convErr == nil {
				e.Status = n
			}
		}
	}
	return e
}
```

> 实现提示：补上 `strconv` 的 import。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add rpc/errors.go rpc/errors_test.go
git commit -m "feat(rpc): map the business errors to the gRPC statuses"
```

---

### Task 4: 核心包导出校验函数与熔断/限流守卫

**Files:**
- Modify: `sprout/server.go`（抽出包级 `buildInputEntityValidateFuncs`）
- Modify: `sprout/interceptor.go`（抽出 `circuitBreakerGuard` / `rateLimiterGuard`）
- Modify: `sprout/api.go`（导出新 API）
- Test: `sprout/interceptor_test.go`（既有测试必须继续通过）

**Interfaces:**
- Consumes: 既有 `validator` / `validateFunc` / `objectValidateFunc` / `circuitBreakerSettings` / `rateLimiterSettings`
- Produces:
  - `func buildInputEntityValidateFuncs(validators []validator, inputType reflect.Type) objectValidateFunc`
  - `func DefaultValidators() []Validator`
  - `func BuildInputEntityValidateFuncs(validators []Validator, inputType reflect.Type) ObjectValidateFunc`
  - `type CircuitBreakerGuard = circuitBreakerGuard` + `func NewCircuitBreakerGuard(name string) *CircuitBreakerGuard` + `func (g *circuitBreakerGuard) Allow() (func(bool), error)`
  - `type RateLimiterGuard = rateLimiterGuard` + `func NewRateLimiterGuard(name string) *RateLimiterGuard` + `func (g *rateLimiterGuard) Allow(clientIdentity string) bool`

- [ ] **Step 1: 写失败的测试**

在 `sprout/rpc/bridge_test.go` 新建（该文件属于 rpc 包，验证核心包导出的 API 可用）：

```go
package rpc

import (
	"reflect"
	"testing"

	sp "github.com/wxy365/sprout"
)

type bridgeInput struct {
	Name string `validate:"required"`
}

func TestCorePackageExportsTheValidatorBuilder(t *testing.T) {
	fn := sp.BuildInputEntityValidateFuncs(sp.DefaultValidators(), reflect.TypeOf(bridgeInput{}))
	if fn == nil {
		t.Fatalf("the validator function should not be nil")
	}
	var in bridgeInput
	if err := fn(nil, reflect.ValueOf(&in).Elem()); err == nil {
		t.Errorf("an empty required field should fail the validation")
	}
	in.Name = "wxy"
	if err := fn(nil, reflect.ValueOf(&in).Elem()); err != nil {
		t.Errorf("a valid input should pass the validation, got %v", err)
	}
}

func TestCorePackageExportsTheGuards(t *testing.T) {
	cb := sp.NewCircuitBreakerGuard("bridge-test")
	if cb == nil {
		t.Fatalf("the circuit breaker guard should not be nil")
	}
	done, err := cb.Allow()
	if err != nil {
		t.Fatalf("the first call should be allowed, got %v", err)
	}
	done(true)

	rl := sp.NewRateLimiterGuard("limiter-test")
	if rl == nil {
		t.Fatalf("the rate limiter guard should not be nil")
	}
	if !rl.Allow("") {
		t.Errorf("the first call should be allowed")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— `sp.BuildInputEntityValidateFuncs` 未定义。

- [ ] **Step 3: 实现核心包改动**

`sprout/server.go`：把 `buildInputEntityValidateFuncs` 的方法体搬到包级函数，方法保留为薄封装。

```go
// buildInputEntityValidateFuncs 按给定的校验器列表构建入参校验函数。
func buildInputEntityValidateFuncs(validators []validator, inputType reflect.Type) objectValidateFunc {
	// 原方法体，把 s.Validators 换成 validators，把 s.buildInputEntityValidateFuncs 换成 buildInputEntityValidateFuncs
}

// buildInputEntityValidateFuncs 构建该服务器上入参类型的校验函数。
func (s *httpServer) buildInputEntityValidateFuncs(inputType reflect.Type) objectValidateFunc {
	return buildInputEntityValidateFuncs(s.Validators, inputType)
}
```

`sprout/interceptor.go`：抽出两个守卫。

```go
// circuitBreakerGuard 封装熔断器的放行与结果上报。
type circuitBreakerGuard struct {
	breaker *gobreaker.TwoStepCircuitBreaker
}

// NewCircuitBreakerGuard 按名称创建熔断守卫，配置取自 app.breakers。
func newCircuitBreakerGuard(breakerName string) *circuitBreakerGuard {
	cbs := getCircuitBreakerSettings(breakerName)
	if cbs == nil {
		cbs = &circuitBreakerSettings{
			MaxRequests:            5,
			Interval:               15 * time.Second,
			Timeout:                15 * time.Second,
			MaxConsecutiveFailures: 10,
			MaxFailureRatio:        0.6,
		}
	}
	return &circuitBreakerGuard{breaker: newCircuitBreaker(cbs)}
}

// Allow 放行一次调用，返回的结果上报函数用于标记本次调用是否成功。
func (g *circuitBreakerGuard) Allow() (func(bool), error) {
	return g.breaker.Allow()
}

// rateLimiterGuard 封装令牌桶的放行判断。
type rateLimiterGuard struct {
	serverLimiter   *rate.Limiter
	clientLimiters  *cache.Cache
	clientLimiter   *rate.Limiter
	defaultExpiration time.Duration
}

// NewRateLimiterGuard 按名称创建限流守卫，配置取自 app.limiters。
func newRateLimiterGuard(limiterName string) *rateLimiterGuard {
	// 原 newRateLimiterInterceptor 中的配置读取与限流器构造部分
}

// Allow 放行一次调用。clientIdentity 为空表示不做客户端维度限流。
func (g *rateLimiterGuard) Allow(clientIdentity string) bool {
	if !g.serverLimiter.Allow() {
		return false
	}
	if g.clientLimiters == nil || clientIdentity == "" {
		return true
	}
	var limiter *rate.Limiter
	if cached, exists := g.clientLimiters.Get(clientIdentity); exists {
		limiter = cached.(*rate.Limiter)
	}
	if limiter == nil {
		limiter = rate.NewLimiter(g.clientTokenRate, g.clientTokenBucketSize)
		g.clientLimiters.Set(clientIdentity, limiter, g.defaultExpiration)
	}
	return limiter.Allow()
}
```

`sprout/api.go` 追加导出：

```go
	// CircuitBreakerGuard 封装熔断器的放行与结果上报。
	//
	// See interceptor.go for the implementation.
	CircuitBreakerGuard = circuitBreakerGuard
	// RateLimiterGuard 封装令牌桶的放行判断。
	//
	// See interceptor.go for the implementation.
	RateLimiterGuard = rateLimiterGuard
```

```go
// DefaultValidators 返回框架内置的校验器列表。
func DefaultValidators() []Validator {
	return defaultValidators()
}

// BuildInputEntityValidateFuncs 按给定的校验器列表构建入参校验函数。
func BuildInputEntityValidateFuncs(validators []Validator, inputType reflect.Type) ObjectValidateFunc {
	return buildInputEntityValidateFuncs(validators, inputType)
}

// NewCircuitBreakerGuard 按名称创建熔断守卫。
func NewCircuitBreakerGuard(name string) *CircuitBreakerGuard {
	return newCircuitBreakerGuard(name)
}

// NewRateLimiterGuard 按名称创建限流守卫。
func NewRateLimiterGuard(name string) *RateLimiterGuard {
	return newRateLimiterGuard(name)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./...`
Expected: PASS —— 既有测试全部通过（HTTP 行为未变），新增桥接测试通过。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add server.go interceptor.go api.go rpc/bridge_test.go
git commit -m "feat: export the validator builder and the rate limiting guards"
```

---

### Task 5: 服务定义与一元服务端

**Files:**
- Create: `sprout/rpc/service.go`
- Test: `sprout/rpc/service_test.go`

**Interfaces:**
- Consumes: Task 1 的 `buildFileDescriptor`，Task 2 的 `toMessage` / `fromMessage`，Task 3 的 `toStatus`，Task 4 的 `sp.BuildInputEntityValidateFuncs`
- Produces:
  - `type Service struct { Name string; Methods []Method }`
  - `type Method interface{ ... }`
  - `func Unary[I, O any](name string, handler func(ctx *Context, in *I) (*O, error)) Method`
  - `type Context struct { context.Context; md metadata.MD }` + `func (c *Context) MD() metadata.MD`
  - `func (s *Service) buildServiceDesc() (*grpc.ServiceDesc, error)`

- [ ] **Step 1: 写失败的测试**

新建 `sprout/rpc/service_test.go`：

```go
package rpc

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

type svcHelloReq struct {
	Name string `pb:"1"`
}

type svcHelloResp struct {
	Greeting string `pb:"1"`
}

func TestServiceBuildsTheServiceDesc(t *testing.T) {
	svc := &Service{
		Name: "demo.Greeter",
		Methods: []Method{
			Unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				return &svcHelloResp{Greeting: "hello " + in.Name}, nil
			}),
		},
	}
	desc, err := svc.buildServiceDesc()
	if err != nil {
		t.Fatalf("buildServiceDesc: %v", err)
	}
	if desc.ServiceName != "demo.Greeter" {
		t.Errorf("service name = %q", desc.ServiceName)
	}
	if len(desc.Methods) != 1 {
		t.Fatalf("methods = %d, want 1", len(desc.Methods))
	}
	if desc.Methods[0].MethodName != "Hello" {
		t.Errorf("method name = %q", desc.Methods[0].MethodName)
	}
}

func TestServiceRejectsAnUnknownMethod(t *testing.T) {
	svc := &Service{Name: "demo.Greeter"}
	if _, err := svc.buildServiceDesc(); err == nil {
		t.Errorf("a service without methods should be rejected")
	}
}

func TestContextExposesTheIncomingMetadata(t *testing.T) {
	md := metadata.Pairs("x-client-id", "c1")
	ctx := &Context{Context: context.Background(), md: md}
	if got := ctx.MD().Get("x-client-id"); len(got) != 1 || got[0] != "c1" {
		t.Errorf("metadata = %v", ctx.MD())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— `Service` / `Method` / `Unary` / `Context` 未定义。

- [ ] **Step 3: 实现服务定义**

新建 `sprout/rpc/service.go`：

```go
package rpc

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	sp "github.com/wxy365/sprout"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Context 是 gRPC 调用的上下文。
type Context struct {
	context.Context
	// md 是客户端传入的元数据。
	md metadata.MD
}

// MD 返回客户端传入的元数据。
func (c *Context) MD() metadata.MD {
	return c.md
}

// Method 描述一个 gRPC 方法。由 Unary 与 Stream 构造。
type Method interface {
	// methodName 返回方法名。
	methodName() string
	// methodKind 返回方法的通信模型。
	methodKind() methodKind
	// goTypes 返回入参与出参的 Go 类型。
	goTypes() (reflect.Type, reflect.Type)
	// buildHandler 生成该方法的 grpc 处理器描述。
	// 一元方法返回 grpc.MethodDesc，流方法返回 grpc.StreamDesc。
	buildHandler(inDesc, outDesc protoreflect.MessageDescriptor, validateFunc sp.ObjectValidateFunc) (grpc.MethodDesc, grpc.StreamDesc, error)
}

// methodKind 是方法的通信模型。
type methodKind int

const (
	// kindUnary 是一元调用。
	kindUnary methodKind = iota
	// kindStream 是双向流调用。
	kindStream
)

// Service 描述一个 gRPC 服务及其方法。
type Service struct {
	// Name 是服务的全限定名，例如 "demo.Greeter"。
	Name string
	// Methods 是该服务的方法列表。
	Methods []Method
}

// unaryMethod 是一元方法的实现。
type unaryMethod[I any, O any] struct {
	name    string
	handler func(ctx *Context, in *I) (*O, error)
}

// Unary 构造一个一元方法。I 是请求类型，O 是响应类型。
func Unary[I, O any](name string, handler func(ctx *Context, in *I) (*O, error)) Method {
	return &unaryMethod[I, O]{name: name, handler: handler}
}

func (m *unaryMethod[I, O]) methodName() string { return m.name }

func (m *unaryMethod[I, O]) methodKind() methodKind { return kindUnary }

func (m *unaryMethod[I, O]) goTypes() (reflect.Type, reflect.Type) {
	return reflect.TypeOf((*I)(nil)).Elem(), reflect.TypeOf((*O)(nil)).Elem()
}

func (m *unaryMethod[I, O]) buildHandler(inDesc, outDesc protoreflect.MessageDescriptor, validateFunc sp.ObjectValidateFunc) (grpc.MethodDesc, grpc.StreamDesc, error) {
	return grpc.MethodDesc{
		MethodName: m.name,
		Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			inMsg := newDynamicMessage(inDesc)
			if err := dec(inMsg); err != nil {
				return nil, err
			}
			in := reflect.New(reflect.TypeOf((*I)(nil)).Elem())
			if err := fromMessage(inMsg, in.Elem()); err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			if validateFunc != nil {
				if err := validateFunc(ctx, in.Elem()); err != nil {
					return nil, toStatus(err)
				}
			}
			out := reflect.New(reflect.TypeOf((*O)(nil)).Elem())
			callCtx := &Context{Context: ctx, md: metadataFromIncoming(ctx)}
			res, err := m.handler(callCtx, in.Interface())
			if err != nil {
				return nil, toStatus(err)
			}
			outMsg := newDynamicMessage(outDesc)
			if err := toMessage(reflect.ValueOf(res).Elem(), outMsg); err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			return outMsg, nil
		},
	}, grpc.StreamDesc{}, nil
}

// servicePackage 从服务名推导 proto 包名与服务名。
func servicePackage(serviceName string) (pkg string, name string, err error) {
	idx := strings.LastIndex(serviceName, ".")
	if idx < 0 {
		return "", "", fmt.Errorf("the service name %q must be qualified with a package, e.g. demo.Greeter", serviceName)
	}
	pkg = serviceName[:idx]
	name = serviceName[idx+1:]
	if pkg == "" || name == "" {
		return "", "", fmt.Errorf("the service name %q is invalid", serviceName)
	}
	return pkg, name, nil
}

// buildServiceDesc 把服务定义拼装成 grpc-go 的服务描述符。
func (s *Service) buildServiceDesc() (*grpc.ServiceDesc, error) {
	pkg, name, err := servicePackage(s.Name)
	if err != nil {
		return nil, err
	}
	if len(s.Methods) == 0 {
		return nil, fmt.Errorf("the service %s has no method", s.Name)
	}
	seen := map[string]bool{}
	desc := &grpc.ServiceDesc{ServiceName: s.Name}
	for _, m := range s.Methods {
		if m.methodName() == "" {
			return nil, fmt.Errorf("the service %s has a method without a name", s.Name)
		}
		if seen[m.methodName()] {
			return nil, fmt.Errorf("the service %s has a duplicated method %s", s.Name, m.methodName())
		}
		seen[m.methodName()] = true
		inType, outType := m.goTypes()
		_, msgs, err := buildFileDescriptor(pkg, pkg+"/"+strings.ToLower(name)+".proto", []reflect.Type{inType, outType})
		if err != nil {
			return nil, fmt.Errorf("the service %s: %w", s.Name, err)
		}
		inDesc := msgs[pkg+"."+inType.Name()]
		outDesc := msgs[pkg+"."+outType.Name()]
		if inDesc == nil || outDesc == nil {
			return nil, fmt.Errorf("the service %s: the message descriptors are not found", s.Name)
		}
		validateFunc := sp.BuildInputEntityValidateFuncs(sp.DefaultValidators(), inType)
		unaryDesc, streamDesc, err := m.buildHandler(inDesc, outDesc, validateFunc)
		if err != nil {
			return nil, fmt.Errorf("the service %s: the method %s: %w", s.Name, m.methodName(), err)
		}
		switch m.methodKind() {
		case kindUnary:
			desc.Methods = append(desc.Methods, unaryDesc)
		case kindStream:
			desc.Streams = append(desc.Streams, streamDesc)
		default:
			return nil, fmt.Errorf("the service %s: the method kind of %s is not supported", s.Name, m.methodName())
		}
	}
	return desc, nil
}

// metadataFromIncoming 取出调用方传入的元数据。
func metadataFromIncoming(ctx context.Context) metadata.MD {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return metadata.MD{}
	}
	return md
}
```

> 实现提示：补上 `protoreflect`、`codes`、`status` 的 import。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add rpc/service.go rpc/service_test.go
git commit -m "feat(rpc): define the services and the unary handlers"
```

---

### Task 6: gRPC 服务端实例与应用生命周期接入

**Files:**
- Create: `sprout/rpc/server.go`
- Modify: `sprout/app.go`（新增 `Runner` 与 `Runners`）
- Modify: `sprout/api.go`（导出 `Runner`）
- Test: `sprout/rpc/server_test.go`

**Interfaces:**
- Consumes: Task 5 的 `Service.buildServiceDesc`
- Produces:
  - `type Server struct { Name string; Port uint16; CertFile string; KeyFile string; Debug bool; ShutdownTimeout time.Duration }`
  - `func NewServer(name string) *Server`
  - `func ServersFromConfig() []*Server`
  - `func (s *Server) Start() error`
  - `func (s *Server) Stop(ctx context.Context) error`
  - `func Mount[C any](svc *Service, a *sp.App[C], svrName ...string)`
  - 核心包：`type Runner interface { Start() error; Stop(ctx context.Context) error }` 与 `app.Runners []Runner`

- [ ] **Step 1: 写失败的测试**

新建 `sprout/rpc/server_test.go`：

```go
package rpc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestServerServesTheMountedService(t *testing.T) {
	srv := &Server{Name: "test", Port: 0, ShutdownTimeout: time.Second}
	srv.addService(&Service{
		Name: "demo.Greeter",
		Methods: []Method{
			Unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				return &svcHelloResp{Greeting: "hello " + in.Name}, nil
			}),
		},
	})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.serve(lis)
	defer srv.Stop(context.Background())

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	_, msgs, err := buildFileDescriptor("demo", "demo/greeter.proto", []reflect.Type{reflect.TypeOf(svcHelloReq{})})
	if err != nil {
		t.Fatalf("buildFileDescriptor: %v", err)
	}
	in := newDynamicMessage(msgs["demo.svcHelloReq"])
	if err := toMessage(reflect.ValueOf(svcHelloReq{Name: "wxy"}), in); err != nil {
		t.Fatalf("toMessage: %v", err)
	}
	out := newDynamicMessage(msgs["demo.svcHelloResp"])
	if err := conn.Invoke(context.Background(), "/demo.Greeter/Hello", in, out); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	var resp svcHelloResp
	if err := fromMessage(out, reflect.ValueOf(&resp).Elem()); err != nil {
		t.Fatalf("fromMessage: %v", err)
	}
	if resp.Greeting != "hello wxy" {
		t.Errorf("greeting = %q", resp.Greeting)
	}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— `Server` 未定义。

- [ ] **Step 3: 实现服务端实例**

新建 `sprout/rpc/server.go`：

```go
package rpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/wxy365/basal/cfg/def"
	"github.com/wxy365/basal/log"
	sp "github.com/wxy365/sprout"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Server 是一个 gRPC 服务端实例。
type Server struct {
	// Name 是服务端名称，用于挂载服务与配置匹配。
	Name string
	// Port 是监听端口，0 表示由系统分配。
	Port uint16
	// CertFile 是 TLS 证书文件路径，与 KeyFile 同时设置时启用 TLS。
	CertFile string
	// KeyFile 是 TLS 私钥文件路径。
	KeyFile string
	// Debug 打开方法级的调试日志。
	Debug bool
	// ShutdownTimeout 是优雅停止的等待时长。
	ShutdownTimeout time.Duration

	mu       sync.Mutex
	services []*Service
	server   *grpc.Server
}

// serverCfg 是 app.grpc_servers 配置项。
type serverCfg struct {
	Name            string `map:"name"`
	Port            uint16 `map:"port"`
	CertFile        string `map:"cert_file"`
	KeyFile         string `map:"key_file"`
	Debug           *bool  `map:"debug"`
	ShutdownTimeout uint64 `map:"shutdown_timeout"`
}

// NewServer 创建一个 gRPC 服务端实例，并从 app.grpc_servers 配置补全同名项。
func NewServer(name string) *Server {
	s := &Server{Name: name, ShutdownTimeout: 10 * time.Second}
	s.applyConfig()
	return s
}

// ServersFromConfig 按 app.grpc_servers 配置创建全部 gRPC 服务端实例。
func ServersFromConfig() []*Server {
	cfgs, err := def.GetObj[[]serverCfg]("app.grpc_servers")
	if err != nil {
		return nil
	}
	servers := make([]*Server, 0, len(cfgs))
	for _, c := range cfgs {
		if c.Name == "" {
			continue
		}
		servers = append(servers, NewServer(c.Name))
	}
	return servers
}

// applyConfig 用配置里的同名项补全服务端。
func (s *Server) applyConfig() {
	cfgs, err := def.GetObj[[]serverCfg]("app.grpc_servers")
	if err != nil {
		return
	}
	for _, c := range cfgs {
		if c.Name != s.Name {
			continue
		}
		if c.Port > 0 {
			s.Port = c.Port
		}
		if c.CertFile != "" {
			s.CertFile = c.CertFile
		}
		if c.KeyFile != "" {
			s.KeyFile = c.KeyFile
		}
		if c.Debug != nil {
			s.Debug = *c.Debug
		}
		if c.ShutdownTimeout > 0 {
			s.ShutdownTimeout = time.Millisecond * time.Duration(c.ShutdownTimeout)
		}
	}
}

// addService 挂载一个服务到该服务端。
func (s *Server) addService(svc *Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = append(s.services, svc)
}

// buildGrpcServer 构建 grpc 服务端并注册全部服务。
func (s *Server) buildGrpcServer() (*grpc.Server, error) {
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unaryInterceptors()...),
		grpc.ChainStreamInterceptor(streamInterceptors()...),
	}
	if s.CertFile != "" && s.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(s.CertFile, s.KeyFile)
		if err != nil {
			return nil, errs.Wrap(err, "cannot load the TLS key pair of the server [{0}]", s.Name)
		}
		opts = append(opts, grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}})))
	}
	gs := grpc.NewServer(opts...)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, svc := range s.services {
		desc, err := svc.buildServiceDesc()
		if err != nil {
			return nil, err
		}
		gs.RegisterService(desc, struct{}{})
	}
	return gs, nil
}

// serve 在给定的监听器上提供服务，阻塞直到服务停止。
func (s *Server) serve(lis net.Listener) {
	gs, err := s.buildGrpcServer()
	if err != nil {
		log.ErrorErrF("Cannot build the gRPC server [{0}]", err, s.Name)
		return
	}
	s.mu.Lock()
	s.server = gs
	s.mu.Unlock()
	gs.Serve(lis)
}

// Start 启动服务端并阻塞，直到服务端停止。
func (s *Server) Start() error {
	gs, err := s.buildGrpcServer()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.server = gs
	s.mu.Unlock()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return errs.Wrap(err, "cannot listen on the port [{0}] of the server [{1}]", s.Port, s.Name)
	}
	s.mu.Lock()
	s.Port = uint16(lis.Addr().(*net.TCPAddr).Port)
	s.mu.Unlock()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		ctx, cancel := context.WithTimeout(context.Background(), s.ShutdownTimeout)
		defer cancel()
		s.Stop(ctx)
	}()

	log.Info("The gRPC server [{0}] is started on port: {1}", s.Name, s.Port)
	if err := gs.Serve(lis); err != nil {
		return err
	}
	return nil
}

// Stop 优雅停止服务端。
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	gs := s.server
	s.mu.Unlock()
	if gs == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		gs.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		gs.Stop()
		return ctx.Err()
	}
}

// Mount 把一个 gRPC 服务挂载到应用中指定的 gRPC 服务端上。
// 未指定服务端名时挂到第一个 gRPC 服务端。
func Mount[C any](svc *Service, a *sp.App[C], svrName ...string) {
	a.Initializers = append(a.Initializers, func(app *sp.App[C]) error {
		target, err := findServer(app, svrName...)
		if err != nil {
			return err
		}
		target.addService(svc)
		return nil
	})
}

// findServer 在应用的运行组件中查找 gRPC 服务端。
func findServer[C any](app *sp.App[C], svrName ...string) (*Server, error) {
	for _, r := range app.Runners {
		srv, ok := r.(*Server)
		if !ok {
			continue
		}
		if len(svrName) == 0 {
			return srv, nil
		}
		if srv.Name == svrName[0] {
			return srv, nil
		}
	}
	if len(svrName) == 0 {
		return nil, errs.New("The application has no gRPC server to mount the service [{0}]", ...)
	}
	return nil, errs.New("Failed to mount the service to the gRPC server [{0}]: the server is not defined", svrName[0])
}
```

`sprout/app.go` 改动：

```go
// Runner 是随应用一起启动与停止的组件。
type Runner interface {
	// Start 阻塞运行，直到组件停止。
	Start() error
	// Stop 优雅停止组件。
	Stop(ctx context.Context) error
}
```

在 `app` 结构体里新增：

```go
	// Runners 是随应用一起启动的组件列表，例如 gRPC 服务端。
	Runners []Runner
```

`App.Run()` 的启动部分改为：

```go
		a.init()
		starts := make([]func(), 0, len(a.Servers)+len(a.Runners))
		for _, server := range a.Servers {
			starts = append(starts, server.start)
		}
		for _, runner := range a.Runners {
			starts = append(starts, runner.Start)
		}
		for i := len(starts); i > 0; i-- {
			start := starts[i-1]
			if i > 1 {
				go start()
			} else {
				start()
			}
		}
```

`sprout/api.go` 追加：

```go
	// Runner 是随应用一起启动与停止的组件。
	//
	// See app.go for the implementation.
	Runner = runner
```

并把 `app` 结构体里的 `Runners []Runner` 改为 `Runners []runner`（未导出接口 + api.go 别名），与既有 `App` / `Endpoint` 的别名风格一致。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./...`
Expected: PASS —— 既有测试通过，gRPC 服务端端到端测试通过。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add rpc/server.go rpc/server_test.go app.go api.go
git commit -m "feat(rpc): serve the gRPC services with the application lifecycle"
```

---

### Task 7: 一元客户端

**Files:**
- Create: `sprout/rpc/client.go`
- Test: `sprout/rpc/client_test.go`

**Interfaces:**
- Consumes: Task 1 / 2 / 5 的产物
- Produces:
  - `type Client struct { ... }`
  - `func NewClient(service string, opts ...ClientOption) (*Client, error)`
  - `func (c *Client) Close() error`
  - `func Doer[I, O any](c *Client, method string) func(ctx context.Context, in *I) (*O, error)`
  - `type ClientOption func(*clientOptions)`
  - `func WithDirect(addr string) ClientOption`
  - `func WithBalancer(name string) ClientOption`
  - `func WithTimeout(d time.Duration) ClientOption`
  - `func WithTLS(cfg *tls.Config) ClientOption`

- [ ] **Step 1: 写失败的测试**

新建 `sprout/rpc/client_test.go`：

```go
package rpc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestClientCallsTheUnaryMethod(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &Server{Name: "test", ShutdownTimeout: time.Second}
	srv.addService(&Service{
		Name: "demo.Greeter",
		Methods: []Method{
			Unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				return &svcHelloResp{Greeting: "hello " + in.Name}, nil
			}),
		},
	})
	go srv.serve(lis)
	defer srv.Stop(context.Background())

	client, err := NewClient("demo.Greeter", WithDirect(lis.Addr().String()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	hello := Doer[svcHelloReq, svcHelloResp](client, "Hello")
	resp, err := hello(context.Background(), &svcHelloReq{Name: "wxy"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Greeting != "hello wxy" {
		t.Errorf("greeting = %q", resp.Greeting)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— `Client` 未定义。

- [ ] **Step 3: 实现客户端**

新建 `sprout/rpc/client.go`：

```go
package rpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"reflect"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Client 是一个 gRPC 客户端。
type Client struct {
	// service 是目标服务的全限定名。
	service string
	// conn 是到目标服务的连接。
	conn *grpc.ClientConn
	// timeout 是单次调用的默认超时。
	timeout time.Duration
	// descCache 缓存消息描述符，避免每次调用都重新合成。
	descCache map[string]protoreflect.MessageDescriptor
}

// clientOptions 是客户端的配置。
type clientOptions struct {
	direct    string
	registry  Registry
	balancer  string
	tlsConfig *tls.Config
	timeout   time.Duration
}

// ClientOption 配置客户端。
type ClientOption func(*clientOptions)

// WithDirect 让客户端直连给定的地址。
func WithDirect(addr string) ClientOption {
	return func(o *clientOptions) { o.direct = addr }
}

// WithBalancer 设置负载均衡策略，默认 round_robin。
func WithBalancer(name string) ClientOption {
	return func(o *clientOptions) { o.balancer = name }
}

// WithTimeout 设置单次调用的默认超时。
func WithTimeout(d time.Duration) ClientOption {
	return func(o *clientOptions) { o.timeout = d }
}

// WithTLS 设置 TLS 配置。
func WithTLS(cfg *tls.Config) ClientOption {
	return func(o *clientOptions) { o.tlsConfig = cfg }
}

// NewClient 创建一个针对指定 gRPC 服务的客户端。
func NewClient(service string, opts ...ClientOption) (*Client, error) {
	o := &clientOptions{balancer: "round_robin"}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	target := "passthrough:///" + o.direct
	if o.direct == "" {
		return nil, errs.New("The client of the service [{0}] has no address", service)
	}
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(fmt.Sprintf(`{"loadBalancingConfig":[{"%s":{}}]}`, o.balancer)),
	}
	if o.tlsConfig != nil {
		dialOpts[0] = grpc.WithTransportCredentials(credentials.NewTLS(o.tlsConfig))
	}
	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, errs.Wrap(err, "Cannot connect to the service [{0}]", service)
	}
	return &Client{service: service, conn: conn, timeout: o.timeout, descCache: map[string]protoreflect.MessageDescriptor{}}, nil
}

// Close 关闭连接。
func (c *Client) Close() error {
	return c.conn.Close()
}

// messageDescs 合成并缓存方法的入参与出参描述符。
func (c *Client) messageDescs(method string, inType, outType reflect.Type) (protoreflect.MessageDescriptor, protoreflect.MessageDescriptor, error) {
	pkg, _, err := servicePackage(c.service)
	if err != nil {
		return nil, nil, err
	}
	key := pkg + "." + inType.Name() + "/" + pkg + "." + outType.Name()
	if inDesc, ok := c.descCache[key]; ok {
		return inDesc, c.descCache[key+"#out"], nil
	}
	_, msgs, err := buildFileDescriptor(pkg, pkg+"/"+serviceFileName(c.service)+".proto", []reflect.Type{inType, outType})
	if err != nil {
		return nil, nil, err
	}
	inDesc := msgs[pkg+"."+inType.Name()]
	outDesc := msgs[pkg+"."+outType.Name()]
	if inDesc == nil || outDesc == nil {
		return nil, nil, errs.New("The message descriptors of the method [{0}] are not found", method)
	}
	c.descCache[key] = inDesc
	c.descCache[key+"#out"] = outDesc
	return inDesc, outDesc, nil
}

// Doer 返回一个类型安全的一元调用闭包。
func Doer[I, O any](c *Client, method string) func(ctx context.Context, in *I) (*O, error) {
	return func(ctx context.Context, in *I) (*O, error) {
		inType := reflect.TypeOf((*I)(nil)).Elem()
		outType := reflect.TypeOf((*O)(nil)).Elem()
		inDesc, outDesc, err := c.messageDescs(method, inType, outType)
		if err != nil {
			return nil, err
		}
		if c.timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.timeout)
			defer cancel()
		}
		inMsg := newDynamicMessage(inDesc)
		if err := toMessage(reflect.ValueOf(in).Elem(), inMsg); err != nil {
			return nil, err
		}
		outMsg := newDynamicMessage(outDesc)
		fullMethod := "/" + c.service + "/" + method
		if err := c.conn.Invoke(ctx, fullMethod, inMsg, outMsg); err != nil {
			return nil, fromStatus(err)
		}
		out := new(O)
		if err := fromMessage(outMsg, reflect.ValueOf(out).Elem()); err != nil {
			return nil, err
		}
		return out, nil
	}
}
```

> 实现提示：补上 `errs`、`protoreflect` 的 import；`serviceFileName` 返回服务名的小写形式。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add rpc/client.go rpc/client_test.go
git commit -m "feat(rpc): call the remote services with a typed client"
```

---

### Task 8: 双向流式

**Files:**
- Create: `sprout/rpc/stream.go`
- Modify: `sprout/rpc/service.go`（支持流式方法）
- Modify: `sprout/rpc/client.go`（支持流式调用）
- Test: `sprout/rpc/stream_test.go`

**Interfaces:**
- Consumes: Task 5 / 7 的产物
- Produces:
  - `func Stream[I, O any](name string, handler func(ctx *Context, stream *ServerStream[I, O]) error) Method`
  - `type ServerStream[I any, O any] struct { ... }` + `Recv() (*I, error)` + `Send(out *O) error` + `Context() context.Context`
  - `type ClientStream[I any, O any] struct { ... }` + `Send(in *I) error` + `Recv() (*O, error)` + `CloseSend() error`
  - `func StreamDoer[I, O any](c *Client, method string) func(ctx context.Context) (*ClientStream[I, O], error)`

- [ ] **Step 1: 写失败的测试**

新建 `sprout/rpc/stream_test.go`：

```go
package rpc

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type streamChatReq struct {
	Text string `pb:"1"`
}

type streamChatResp struct {
	Text string `pb:"1"`
}

func TestBidirectionalStream(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &Server{Name: "test", ShutdownTimeout: time.Second}
	srv.addService(&Service{
		Name: "demo.Chat",
		Methods: []Method{
			Stream("Chat", func(ctx *Context, s *ServerStream[streamChatReq, streamChatResp]) error {
				for {
					in, err := s.Recv()
					if err == io.EOF {
						return nil
					}
					if err != nil {
						return err
					}
					if err := s.Send(&streamChatResp{Text: "echo " + in.Text}); err != nil {
						return err
					}
				}
			}),
		},
	})
	go srv.serve(lis)
	defer srv.Stop(context.Background())

	client, err := NewClient("demo.Chat", WithDirect(lis.Addr().String()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	chat := StreamDoer[streamChatReq, streamChatResp](client, "Chat")
	stream, err := chat(context.Background())
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	for _, text := range []string{"a", "b"} {
		if err := stream.Send(&streamChatReq{Text: text}); err != nil {
			t.Fatalf("send: %v", err)
		}
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if resp.Text != "echo "+text {
			t.Errorf("text = %q, want %q", resp.Text, "echo "+text)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close send: %v", err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Errorf("the stream should end with io.EOF, got %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— `Stream` / `ServerStream` / `ClientStream` / `StreamDoer` 未定义。

- [ ] **Step 3: 实现流式**

新建 `sprout/rpc/stream.go`：

```go
package rpc

import (
	"context"
	"fmt"
	"io"
	"reflect"

	"google.golang.org/grpc"
)

// ServerStream 是服务端的双向流句柄。
type ServerStream[I any, O any] struct {
	// stream 是底层的 grpc 流。
	stream grpc.ServerStream
	// inDesc 是入参的消息描述符。
	inDesc protoreflect.MessageDescriptor
	// outDesc 是出参的消息描述符。
	outDesc protoreflect.MessageDescriptor
}

// Recv 接收一条消息。客户端半关闭后返回 io.EOF。
func (s *ServerStream[I, O]) Recv() (*I, error) {
	inMsg := newDynamicMessage(s.inDesc)
	if err := s.stream.RecvMsg(inMsg); err != nil {
		return nil, err
	}
	in := new(I)
	if err := fromMessage(inMsg, reflect.ValueOf(in).Elem()); err != nil {
		return nil, err
	}
	return in, nil
}

// Send 发送一条消息。
func (s *ServerStream[I, O]) Send(out *O) error {
	outMsg := newDynamicMessage(s.outDesc)
	if err := toMessage(reflect.ValueOf(out).Elem(), outMsg); err != nil {
		return err
	}
	return s.stream.SendMsg(outMsg)
}

// Context 返回该流的上下文。
func (s *ServerStream[I, O]) Context() context.Context {
	return s.stream.Context()
}

// ClientStream 是客户端的双向流句柄。
type ClientStream[I any, O any] struct {
	// stream 是底层的 grpc 流。
	stream grpc.ClientStream
	// inDesc 是入参的消息描述符。
	inDesc protoreflect.MessageDescriptor
	// outDesc 是出参的消息描述符。
	outDesc protoreflect.MessageDescriptor
}

// Send 发送一条消息。
func (s *ClientStream[I, O]) Send(in *I) error {
	inMsg := newDynamicMessage(s.inDesc)
	if err := toMessage(reflect.ValueOf(in).Elem(), inMsg); err != nil {
		return err
	}
	return s.stream.SendMsg(inMsg)
}

// Recv 接收一条消息。流结束时返回 io.EOF。
func (s *ClientStream[I, O]) Recv() (*O, error) {
	outMsg := newDynamicMessage(s.outDesc)
	if err := s.stream.RecvMsg(outMsg); err != nil {
		return nil, err
	}
	out := new(O)
	if err := fromMessage(outMsg, reflect.ValueOf(out).Elem()); err != nil {
		return nil, err
	}
	return out, nil
}

// CloseSend 关闭发送方向，通知服务端不再有后续消息。
func (s *ClientStream[I, O]) CloseSend() error {
	return s.stream.CloseSend()
}

// Context 返回该流的上下文。
func (s *ClientStream[I, O]) Context() context.Context {
	return s.stream.Context()
}

// streamMethod 是双向流方法的实现。
type streamMethod[I any, O any] struct {
	name    string
	handler func(ctx *Context, stream *ServerStream[I, O]) error
}

// Stream 构造一个双向流方法。
func Stream[I, O any](name string, handler func(ctx *Context, stream *ServerStream[I, O]) error) Method {
	return &streamMethod[I, O]{name: name, handler: handler}
}

func (m *streamMethod[I, O]) methodName() string { return m.name }

func (m *streamMethod[I, O]) methodKind() methodKind { return kindStream }

func (m *streamMethod[I, O]) goTypes() (reflect.Type, reflect.Type) {
	return reflect.TypeOf((*I)(nil)).Elem(), reflect.TypeOf((*O)(nil)).Elem()
}

// buildHandler 生成流方法的 grpc 处理器描述。
func (m *streamMethod[I, O]) buildHandler(inDesc, outDesc protoreflect.MessageDescriptor, validateFunc sp.ObjectValidateFunc) (grpc.MethodDesc, grpc.StreamDesc, error) {
	return grpc.MethodDesc{}, grpc.StreamDesc{
		StreamName: m.name,
		Handler: func(srv any, stream grpc.ServerStream) error {
			ss := &ServerStream[I, O]{stream: stream, inDesc: inDesc, outDesc: outDesc}
			callCtx := &Context{Context: stream.Context(), md: metadataFromIncoming(stream.Context())}
			if err := m.handler(callCtx, ss); err != nil {
				return toStatus(err)
			}
			return nil
		},
		ServerStreams: true,
		ClientStreams: true,
	}, nil
}
```

`ServerStream[I,O]` 直接持有 `grpc.ServerStream` 与两个消息描述符，
泛型只体现在 `Recv` 返回 `*I`、`Send` 接收 `*O` 上，无需额外的非泛型中间层。

客户端侧在 `client.go` 追加：

```go
// StreamDoer 返回一个类型安全的双向流调用闭包。
func StreamDoer[I, O any](c *Client, method string) func(ctx context.Context) (*ClientStream[I, O], error) {
	return func(ctx context.Context) (*ClientStream[I, O], error) {
		inType := reflect.TypeOf((*I)(nil)).Elem()
		outType := reflect.TypeOf((*O)(nil)).Elem()
		inDesc, outDesc, err := c.messageDescs(method, inType, outType)
		if err != nil {
			return nil, err
		}
		if c.timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.timeout)
		}
		fullMethod := "/" + c.service + "/" + method
		stream, err := c.conn.NewStream(ctx, &grpc.StreamDesc{
			StreamName:    method,
			ServerStreams: true,
			ClientStreams: true,
		}, fullMethod)
		if err != nil {
			return nil, fromStatus(err)
		}
		return &ClientStream[I, O]{stream: stream, inDesc: inDesc, outDesc: outDesc}, nil
	}
}
```

`buildServiceDesc` 在 Task 5 中已经按 `methodKind()` 分派 `buildHandler` 的返回值，
因此这里不需要再改 `service.go` 的分派逻辑。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add rpc/stream.go rpc/stream_test.go rpc/service.go rpc/client.go
git commit -m "feat(rpc): support the bidirectional streaming"
```

---

### Task 9: 注册中心接口与 resolver

**Files:**
- Create: `sprout/rpc/resolver.go`
- Test: `sprout/rpc/resolver_test.go`

**Interfaces:**
- Consumes: Task 7 的客户端
- Produces:
  - `type Instance struct { Addr string; Healthy bool; Weight float64; Metadata map[string]string }`
  - `type Registry interface { Discover(ctx context.Context, service string) (<-chan []Instance, error) }`
  - `func WithRegistry(r Registry) ClientOption`
  - `func WithRegistryService(name string) ClientOption`（注册中心侧的服务名，默认与 gRPC 服务名相同）

- [ ] **Step 1: 写失败的测试**

新建 `sprout/rpc/resolver_test.go`：

```go
package rpc

import (
	"context"
	"net"
	"testing"
	"time"
)

// fakeRegistry 是一个可控的注册中心。
type fakeRegistry struct {
	ch chan []Instance
}

func (r *fakeRegistry) Discover(ctx context.Context, service string) (<-chan []Instance, error) {
	return r.ch, nil
}

func TestClientResolvesTheAddressesFromTheRegistry(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &Server{Name: "test", ShutdownTimeout: time.Second}
	srv.addService(&Service{
		Name: "demo.Greeter",
		Methods: []Method{
			Unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				return &svcHelloResp{Greeting: "hello " + in.Name}, nil
			}),
		},
	})
	go srv.serve(lis)
	defer srv.Stop(context.Background())

	reg := &fakeRegistry{ch: make(chan []Instance, 1)}
	reg.ch <- []Instance{{Addr: lis.Addr().String(), Healthy: true}}

	client, err := NewClient("demo.Greeter", WithRegistry(reg))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	hello := Doer[svcHelloReq, svcHelloResp](client, "Hello")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := hello(ctx, &svcHelloReq{Name: "wxy"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Greeting != "hello wxy" {
		t.Errorf("greeting = %q", resp.Greeting)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— `Registry` / `WithRegistry` 未定义。

- [ ] **Step 3: 实现 resolver**

新建 `sprout/rpc/resolver.go`：

```go
package rpc

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"github.com/wxy365/basal/log"
	"google.golang.org/grpc/resolver"
)

// registryScheme 是走注册中心寻址时使用的目标 scheme。
const registryScheme = "sprout"

// Instance 是一个服务提供者实例。
type Instance struct {
	// Addr 是实例地址，形如 host:port。
	Addr string
	// Healthy 表示实例是否健康。
	Healthy bool
	// Weight 是实例权重。
	Weight float64
	// Metadata 是实例元数据。
	Metadata map[string]string
}

// Registry 把服务名解析为服务提供者实例列表。
type Registry interface {
	// Discover 订阅指定服务的实例列表，返回实例更新的通道。通道关闭表示订阅结束。
	Discover(ctx context.Context, service string) (<-chan []Instance, error)
}

// WithRegistry 让客户端通过给定的注册中心发现服务提供者。
func WithRegistry(r Registry) ClientOption {
	return func(o *clientOptions) { o.registry = r }
}

// WithRegistryService 设置注册中心侧的服务名，默认与 gRPC 服务名相同。
func WithRegistryService(name string) ClientOption {
	return func(o *clientOptions) { o.registryService = name }
}

// registryResolverBuilder 把注册中心的实例列表推送给 grpc-go。
type registryResolverBuilder struct {
	registry Registry
}

// Scheme 返回该 builder 支持的 scheme。
func (b *registryResolverBuilder) Scheme() string { return registryScheme }

// Build 为一个目标构建 resolver。
func (b *registryResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	service := target.URL.Path
	if service == "" {
		service = strings.TrimPrefix(target.URL.String(), "/")
	}
	q := target.URL.Query()
	if v := q.Get("service"); v != "" {
		service = v
	}
	ctx, cancel := context.WithCancel(context.Background())
	instances, err := b.registry.Discover(ctx, service)
	if err != nil {
		cancel()
		return nil, err
	}
	r := &registryResolver{cc: cc, cancel: cancel}
	go r.watch(ctx, instances)
	return r, nil
}

// registryResolver 把注册中心的实例变化转成 grpc 的地址更新。
type registryResolver struct {
	cc     resolver.ClientConn
	cancel context.CancelFunc
}

// watch 读取实例更新并推送给 grpc-go。
func (r *registryResolver) watch(ctx context.Context, instances <-chan []Instance) {
	for {
		select {
		case <-ctx.Done():
			return
		case list, ok := <-instances:
			if !ok {
				return
			}
			r.cc.UpdateState(resolver.State{Addresses: toResolverAddresses(list)})
		}
	}
}

// ResolveNow 提示重新解析，本实现的解析由注册中心推送驱动，因此不做处理。
func (r *registryResolver) ResolveNow(resolver.ResolveNowOptions) {}

// Close 停止解析。
func (r *registryResolver) Close() {
	r.cancel()
}

// toResolverAddresses 把实例列表转成 grpc 的地址列表。
func toResolverAddresses(list []Instance) []resolver.Address {
	addrs := make([]resolver.Address, 0, len(list))
	for _, i := range list {
		if !i.Healthy {
			continue
		}
		addrs = append(addrs, resolver.Address{Addr: i.Addr})
	}
	return addrs
}

// registryTarget 构造走注册中心的目标串。
func registryTarget(service string) string {
	return registryScheme + ":///" + url.PathEscape(service)
}
```

在 `client.go` 的 `NewClient` 中处理注册中心分支：

```go
	target := ""
	dialOpts := []grpc.DialOption{}
	if o.registry != nil {
		target = registryTarget(o.registryService)
		if target == registryScheme+":///" {
			target = registryTarget(service)
		}
		dialOpts = append(dialOpts, grpc.WithResolvers(&registryResolverBuilder{registry: o.registry}))
	} else {
		if o.direct == "" {
			return nil, errs.New("The client of the service [{0}] has no address", service)
		}
		target = "passthrough:///" + o.direct
	}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add rpc/resolver.go rpc/resolver_test.go rpc/client.go
git commit -m "feat(rpc): discover the service providers through a registry"
```

---

### Task 10: Nacos 注册中心适配

**Files:**
- Create: `sprout/rpc/nacos.go`
- Test: `sprout/rpc/nacos_test.go`

**Interfaces:**
- Consumes: Task 9 的 `Registry` / `Instance`
- Produces:
  - `type NacosConfig struct { Servers []string; Namespace string; Group string; Username string; Password string; Interval time.Duration; Service string }`
  - `func WithNacos(cfg NacosConfig) ClientOption`
  - `func NewNacosRegistry(cfg NacosConfig) (Registry, error)`

- [ ] **Step 1: 写失败的测试**

新建 `sprout/rpc/nacos_test.go`：

```go
package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNacosRegistryDiscoversTheInstances(t *testing.T) {
	var hosts = []map[string]any{
		{"ip": "10.0.0.1", "port": 8080, "healthy": true, "enabled": true, "weight": 1.0},
		{"ip": "10.0.0.2", "port": 8080, "healthy": false, "enabled": true, "weight": 1.0},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/nacos/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"accessToken": "tk", "tokenTtl": 18000})
	})
	mux.HandleFunc("/nacos/v1/ns/instance/list", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("serviceName"); got != "demo-greeter" {
			t.Errorf("serviceName = %q", got)
		}
		if got := r.URL.Query().Get("groupName"); got != "DEFAULT_GROUP" {
			t.Errorf("groupName = %q", got)
		}
		json.NewEncoder(w).Encode(map[string]any{"hosts": hosts})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg, err := NewNacosRegistry(NacosConfig{
		Servers:  []string{srv.URL},
		Group:    "DEFAULT_GROUP",
		Interval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewNacosRegistry: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := reg.Discover(ctx, "demo-greeter")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	select {
	case list := <-ch:
		if len(list) != 2 {
			t.Fatalf("instances = %d, want 2", len(list))
		}
		if list[0].Addr != "10.0.0.1:8080" {
			t.Errorf("addr = %q", list[0].Addr)
		}
		if list[1].Healthy {
			t.Errorf("the second instance should be unhealthy")
		}
	case <-ctx.Done():
		t.Fatalf("no instance list received")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— `NacosConfig` / `NewNacosRegistry` 未定义。

- [ ] **Step 3: 实现 Nacos 适配**

新建 `sprout/rpc/nacos.go`：

```go
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wxy365/basal/log"
)

// NacosConfig 是 Nacos 注册中心的配置。
type NacosConfig struct {
	// Servers 是 Nacos 服务端地址列表，例如 "127.0.0.1:8848"。
	Servers []string
	// Namespace 是命名空间 ID。
	Namespace string
	// Group 是服务分组，默认 DEFAULT_GROUP。
	Group string
	// Username 是登录用户名。
	Username string
	// Password 是登录密码。
	Password string
	// Interval 是实例列表的轮询间隔，默认 5 秒。
	Interval time.Duration
	// Service 是注册中心侧的服务名，默认与 gRPC 服务名相同。
	Service string
}

// WithNacos 让客户端通过 Nacos 发现服务提供者。
func WithNacos(cfg NacosConfig) ClientOption {
	return func(o *clientOptions) {
		o.registryService = cfg.Service
		o.nacos = &cfg
	}
}

// nacosRegistry 通过 Nacos 的开放 HTTP API 发现服务提供者。
type nacosRegistry struct {
	cfg    NacosConfig
	client *http.Client
}

// NewNacosRegistry 创建一个 Nacos 注册中心。
func NewNacosRegistry(cfg NacosConfig) (Registry, error) {
	if len(cfg.Servers) == 0 {
		return nil, errs.New("The Nacos registry has no server")
	}
	if cfg.Group == "" {
		cfg.Group = "DEFAULT_GROUP"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	return &nacosRegistry{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}, nil
}

// Discover 订阅实例列表，按间隔轮询并在实例集合变化时推送。
func (r *nacosRegistry) Discover(ctx context.Context, service string) (<-chan []Instance, error) {
	ch := make(chan []Instance, 1)
	go r.poll(ctx, service, ch)
	return ch, nil
}

// poll 轮询实例列表。
func (r *nacosRegistry) poll(ctx context.Context, service string, ch chan<- []Instance) {
	defer close(ch)
	var last []Instance
	for {
		list, err := r.instances(ctx, service)
		if err != nil {
			log.WarnErrF("Failed to discover the instances of the service [{0}] from Nacos", err, service)
		} else if !sameInstances(last, list) {
			last = list
			select {
			case ch <- list:
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.cfg.Interval):
		}
	}
}

// instances 查询一次实例列表。
func (r *nacosRegistry) instances(ctx context.Context, service string) ([]Instance, error) {
	token, err := r.login(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("serviceName", service)
	q.Set("groupName", r.cfg.Group)
	q.Set("healthyOnly", "false")
	if r.cfg.Namespace != "" {
		q.Set("namespaceId", r.cfg.Namespace)
	}
	if token != "" {
		q.Set("accessToken", token)
	}
	body, err := r.get(ctx, "/nacos/v1/ns/instance/list", q)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Hosts []struct {
			IP      string            `json:"ip"`
			Port    int               `json:"port"`
			Healthy bool              `json:"healthy"`
			Enabled bool              `json:"enabled"`
			Weight  float64           `json:"weight"`
			Meta    map[string]string `json:"metadata"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	list := make([]Instance, 0, len(resp.Hosts))
	for _, h := range resp.Hosts {
		list = append(list, Instance{
			Addr:     net.JoinHostPort(h.IP, strconv.Itoa(h.Port)),
			Healthy:  h.Healthy && h.Enabled,
			Weight:   h.Weight,
			Metadata: h.Meta,
		})
	}
	return list, nil
}

// login 登录 Nacos 并取回访问令牌。
func (r *nacosRegistry) login(ctx context.Context) (string, error) {
	if r.cfg.Username == "" {
		return "", nil
	}
	form := url.Values{}
	form.Set("username", r.cfg.Username)
	form.Set("password", r.cfg.Password)
	body, err := r.post(ctx, "/nacos/v1/auth/login", form)
	if err != nil {
		return "", err
	}
	var resp struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	return resp.AccessToken, nil
}

// get 发起一次 GET 请求。
func (r *nacosRegistry) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL()+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	return r.do(req)
}

// post 发起一次 POST 请求。
func (r *nacosRegistry) post(ctx context.Context, path string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r.do(req)
}

// do 执行请求并读取响应体。
func (r *nacosRegistry) do(req *http.Request) ([]byte, error) {
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errs.New("The Nacos request [{0}] failed with the status [{1}]", req.URL.Path, resp.StatusCode)
	}
	return body, nil
}

// baseURL 轮询服务端地址，返回一个带 scheme 的基地址。
func (r *nacosRegistry) baseURL() string {
	addr := r.cfg.Servers[0]
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return strings.TrimSuffix(addr, "/")
}

// sameInstances 判断两个实例列表是否相同。
func sameInstances(a, b []Instance) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Addr != b[i].Addr || a[i].Healthy != b[i].Healthy {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add rpc/nacos.go rpc/nacos_test.go rpc/client.go
git commit -m "feat(rpc): discover the service providers through Nacos"
```

---

### Task 11: gRPC 拦截器

**Files:**
- Create: `sprout/rpc/interceptor.go`
- Modify: `sprout/rpc/server.go`（接入拦截器）
- Test: `sprout/rpc/interceptor_test.go`

**Interfaces:**
- Consumes: Task 4 的 `sp.NewCircuitBreakerGuard` / `sp.NewRateLimiterGuard`
- Produces:
  - `type UnaryInterceptor = grpc.UnaryServerInterceptor`
  - `type StreamInterceptor = grpc.StreamServerInterceptor`
  - `func unaryInterceptors() []grpc.UnaryServerInterceptor`
  - `func streamInterceptors() []grpc.StreamServerInterceptor`

- [ ] **Step 1: 写失败的测试**

新建 `sprout/rpc/interceptor_test.go`：

```go
package rpc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTheServerRecoversThePanic(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &Server{Name: "test", ShutdownTimeout: time.Second}
	srv.addService(&Service{
		Name: "demo.Greeter",
		Methods: []Method{
			Unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				panic("boom")
			}),
		},
	})
	go srv.serve(lis)
	defer srv.Stop(context.Background())

	client, err := NewClient("demo.Greeter", WithDirect(lis.Addr().String()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	hello := Doer[svcHelloReq, svcHelloResp](client, "Hello")
	_, err = hello(context.Background(), &svcHelloReq{Name: "wxy"})
	if err == nil {
		t.Fatalf("the panic should be converted into an error")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd sprout && env -u GOROOT go test ./rpc/...`
Expected: FAIL —— panic 未被恢复，测试进程崩溃。

- [ ] **Step 3: 实现拦截器**

新建 `sprout/rpc/interceptor.go`：

```go
package rpc

import (
	"context"

	sp "github.com/wxy365/sprout"
	"github.com/wxy365/basal/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryInterceptor 拦截一元调用。
type UnaryInterceptor = grpc.UnaryServerInterceptor

// StreamInterceptor 拦截流式调用。
type StreamInterceptor = grpc.StreamServerInterceptor

// recoverUnaryInterceptor 把 handler 的 panic 转成 Internal 错误。
func recoverUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("Panic recovered in the gRPC method: {0} {1}", info.FullMethod, r)
				err = status.Error(codes.Internal, "Internal Server Error")
			}
		}()
		return handler(ctx, req)
	}
}

// recoverStreamInterceptor 把流处理函数的 panic 转成 Internal 错误。
func recoverStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("Panic recovered in the gRPC stream: {0} {1}", info.FullMethod, r)
				err = status.Error(codes.Internal, "Internal Server Error")
			}
		}()
		return handler(srv, ss)
	}
}

// breakerUnaryInterceptor 按方法名做熔断。
func breakerUnaryInterceptor() grpc.UnaryServerInterceptor {
	guards := map[string]*sp.CircuitBreakerGuard{}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		guard, ok := guards[info.FullMethod]
		if !ok {
			guard = sp.NewCircuitBreakerGuard(info.FullMethod)
			guards[info.FullMethod] = guard
		}
		done, allowErr := guard.Allow()
		if allowErr != nil {
			return nil, status.Error(codes.Unavailable, "the circuit breaker is open")
		}
		var panicked bool
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				done(false)
				panic(r)
			}
			if panicked {
				return
			}
			done(err == nil)
		}()
		return handler(ctx, req)
	}
}

// limiterUnaryInterceptor 按方法名做限流。
func limiterUnaryInterceptor() grpc.UnaryServerInterceptor {
	guards := map[string]*sp.RateLimiterGuard{}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		guard, ok := guards[info.FullMethod]
		if !ok {
			guard = sp.NewRateLimiterGuard(info.FullMethod)
			guards[info.FullMethod] = guard
		}
		if !guard.Allow(clientIdentityFromContext(ctx)) {
			return nil, status.Error(codes.ResourceExhausted, "the request is rate limited")
		}
		return handler(ctx, req)
	}
}

// clientIdentityFromContext 从元数据里取客户端标识。
func clientIdentityFromContext(ctx context.Context) string {
	md, ok := metadataFromIncoming(ctx).(metadata.MD)
	if !ok {
		return ""
	}
	values := md.Get("client-id")
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// unaryInterceptors 返回服务端的一元拦截器链。
func unaryInterceptors() []grpc.UnaryServerInterceptor {
	return []grpc.UnaryServerInterceptor{
		recoverUnaryInterceptor(),
		breakerUnaryInterceptor(),
		limiterUnaryInterceptor(),
	}
}

// streamInterceptors 返回服务端的流拦截器链。
func streamInterceptors() []grpc.StreamServerInterceptor {
	return []grpc.StreamServerInterceptor{
		recoverStreamInterceptor(),
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd sprout && env -u GOROOT go test ./...`
Expected: PASS —— panic 被转成错误，既有测试不受影响。

- [ ] **Step 5: 提交**

```bash
cd sprout
git add rpc/interceptor.go rpc/interceptor_test.go rpc/server.go
git commit -m "feat(rpc): recover the panics and guard the gRPC calls"
```

---

### Task 12: mimo 演示与文档收尾

**Files:**
- Create: `mimo/iface/rpc/demo.go`
- Modify: `mimo/config/config.json`（加 `app.grpc_servers`）
- Modify: `mimo/api_test.http`（加调用示例）
- Modify: `CLAUDE.md`（模块依赖图与 sprout 子包说明）

**Interfaces:**
- Consumes: Task 5 / 7 / 8 的全部产物
- Produces: 无（收尾任务）

- [ ] **Step 1: 写失败的测试**

在 `mimo` 下新增 `mimo/iface/rpc/demo_test.go`，验证演示服务的两个方法都能跑通：

```go
package rpc

import (
	"context"
	"testing"

	"github.com/wxy365/basal/errs"
)

// HelloReq 是演示服务的请求。
type HelloReq struct {
	Name string `pb:"1"`
	Age  int32  `pb:"2"`
}

// HelloResp 是演示服务的响应。
type HelloResp struct {
	Greeting string `pb:"1"`
}

// ChatReq 是演示流的请求。
type ChatReq struct {
	Text string `pb:"1"`
}

// ChatResp 是演示流的响应。
type ChatResp struct {
	Text string `pb:"1"`
}

// DemoService 是演示服务。
type DemoService struct{}

// Hello 返回一句问候。
func (d *DemoService) Hello(ctx *Context, in *HelloReq) (*HelloResp, error) {
	if in.Name == "" {
		return nil, errs.New("The name must not be empty").WithStatus(400)
	}
	return &HelloResp{Greeting: "hello " + in.Name}, nil
}

// Chat 回显每一条消息。
func (d *DemoService) Chat(ctx *Context, s *ServerStream[ChatReq, ChatResp]) error {
	for {
		in, err := s.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.Send(&ChatResp{Text: "echo " + in.Text}); err != nil {
			return err
		}
	}
}

// NewDemoService 构造演示服务。
func NewDemoService() *Service {
	d := &DemoService{}
	return &Service{
		Name: "demo.Greeter",
		Methods: []Method{
			Unary("Hello", d.Hello),
			Stream("Chat", d.Chat),
		},
	}
}
```

> 演示代码放在 `mimo/iface/rpc/demo.go`，上面的测试放在 `mimo/iface/rpc/demo_test.go`，
> 断言 `NewDemoService()` 的服务名与两个方法名正确。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd mimo && env -u GOROOT go test ./iface/rpc/...`
Expected: FAIL —— 目录与文件不存在。

- [ ] **Step 3: 实现演示与文档**

创建 `mimo/iface/rpc/demo.go`（内容同上，去掉测试函数），在 `mimo/config/config.json` 追加：

```json
  "grpc_servers": [
    { "name": "grpc", "port": 9090, "shutdown_timeout": 10000 }
  ]
```

在 `mimo/api_test.http` 追加：

```
### gRPC 演示服务（demo.Greeter）
# 需要先启动 mimo：cd mimo && env -u GOROOT go run ./mimo
# grpcurl -plaintext -d '{"name":"wxy","age":30}' 127.0.0.1:9090 demo.Greeter/Hello
# grpcurl -plaintext -d '{"text":"hi"}' 127.0.0.1:9090 demo.Greeter/Chat
```

更新 `CLAUDE.md`：

- 模块依赖图中给 sprout 补上 grpc / protobuf 依赖说明；
- 新增「sprout/rpc 子包」一节，说明消息定义方式、描述符合成、服务端与客户端用法、Nacos 寻址。

- [ ] **Step 4: 运行全部测试确认通过**

Run: `cd basal && env -u GOROOT go test ./... && cd ../sprout && env -u GOROOT go test ./... && cd ../mimo && env -u GOROOT go test ./...`
Expected: 三个模块的全部测试通过。

- [ ] **Step 5: 提交**

```bash
cd mimo
git add iface/rpc/demo.go iface/rpc/demo_test.go config/config.json api_test.http
git commit -m "feat(rpc): add the gRPC demo service to mimo"
cd ..
# CLAUDE.md 属于工作区根目录，不在 sprout 仓库内，单独确认后提交
```

---

## 验收标准

- `cd sprout && env -u GOROOT go test ./...` 全绿，且 `basal` / `mimo` 的既有测试不受影响。
- `sprout/rpc` 包的描述符合成结果与 protoc 生成的类型线格式逐字节一致（Task 1 的 `TestWireCompatWithProtocGenerated`）。
- 一元调用与双向流调用在真实 gRPC 服务上端到端可用（Task 7 / Task 8）。
- 客户端既能直连地址，也能通过注册中心发现服务提供者（Task 9 / Task 10）。
- 业务错误在服务端转成 gRPC 状态、在客户端还原成 `*errs.Err`（Task 3）。
- HTTP 侧行为零变化：`sprout` 既有测试全部保持通过（Task 4）。
- 全程不使用 `git add .`，每次提交只包含本任务的文件。
