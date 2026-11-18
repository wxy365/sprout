package rpc

import (
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// descAddress 是一个被提升为顶层 message 的嵌套结构体。
type descAddress struct {
	City string `pb:"1"`
	Zip  string `pb:"2"`
}

// descHelloReq 覆盖了受支持的各类字段。
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
		t.Fatalf("the message demo.descHelloReq is not found")
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
			t.Fatalf("the field %d is not found", w.num)
		}
		if fd.Kind() != w.kind {
			t.Errorf("the field %d kind = %s, want %s", w.num, fd.Kind(), w.kind)
		}
	}
	if msgs["demo.descAddress"] == nil {
		t.Errorf("the nested struct should be promoted to a top level message")
	}
	meta := md.Fields().ByNumber(5)
	if !meta.IsMap() {
		t.Errorf("the field 5 should be a map")
	}
	// protobuf 的反射里 map 字段是 Repeated 基数，但 IsList 对 map 返回 false
	if meta.Cardinality() != protoreflect.Repeated {
		t.Errorf("a map field should have the repeated cardinality")
	}
	if !md.Fields().ByNumber(3).IsList() {
		t.Errorf("the field 3 should be repeated")
	}
	if got := md.Fields().ByNumber(1).Name(); got != "Name" {
		t.Errorf("the field name = %q, want %q", got, "Name")
	}
	if got := md.Fields().ByNumber(1).JSONName(); got != "name" {
		t.Errorf("the json name = %q, want %q", got, "name")
	}
	if got := md.Fields().ByNumber(2).JSONName(); got != "age" {
		t.Errorf("the json name = %q, want %q", got, "age")
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
		t.Errorf("a pointer scalar should have an explicit presence")
	}
	if fd.ContainingOneof() == nil {
		t.Fatalf("a proto3 optional field should belong to a synthetic oneof")
	}
	if got := string(fd.ContainingOneof().Name()); got != "_Optional" {
		t.Errorf("the synthetic oneof name = %q, want %q", got, "_Optional")
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
		t.Errorf("the time.Time should map to google.protobuf.Timestamp, got %q", got)
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
			t.Errorf("%s: an error is expected, but got none", c.name)
		}
	}
}

func TestJsonName(t *testing.T) {
	cases := map[string]string{
		"Name":     "name",
		"Age":      "age",
		"UserName": "userName",
		"user_name": "userName",
	}
	for in, want := range cases {
		if got := jsonName(in); got != want {
			t.Errorf("jsonName(%q) = %q, want %q", in, got, want)
		}
	}
}

// 下面这些具名类型与 wrapperspb 中的类型一一对应，用于线格式比对。
type compatString struct {
	Value string `pb:"1"`
}

type compatBytes struct {
	Value []byte `pb:"1"`
}

type compatInt32 struct {
	Value int32 `pb:"1"`
}

type compatInt64 struct {
	Value int64 `pb:"1"`
}

type compatBool struct {
	Value bool `pb:"1"`
}

type compatDouble struct {
	Value float64 `pb:"1"`
}

// TestWireCompatWithProtocGenerated 证明合成出来的描述符与 protoc 生成的类型线格式一致：
// protoc 产出的字节能被合成描述符解析，且重新序列化后逐字节相同。
func TestWireCompatWithProtocGenerated(t *testing.T) {
	cases := []struct {
		name string
		gen  proto.Message
		typ  reflect.Type
	}{
		{"string", wrapperspb.String("hi"), reflect.TypeOf(compatString{})},
		{"bytes", wrapperspb.Bytes([]byte("abc")), reflect.TypeOf(compatBytes{})},
		{"int32", wrapperspb.Int32(42), reflect.TypeOf(compatInt32{})},
		{"int64", wrapperspb.Int64(-9), reflect.TypeOf(compatInt64{})},
		{"bool", wrapperspb.Bool(true), reflect.TypeOf(compatBool{})},
		{"double", wrapperspb.Double(3.5), reflect.TypeOf(compatDouble{})},
	}
	for _, c := range cases {
		_, msgs, err := buildFileDescriptor("compat", "compat/c.proto", []reflect.Type{c.typ})
		if err != nil {
			t.Fatalf("%s: buildFileDescriptor: %v", c.name, err)
		}
		md := msgs["compat."+c.typ.Name()]
		if md == nil {
			t.Fatalf("%s: the message compat.%s is not found", c.name, c.typ.Name())
		}
		want, err := proto.Marshal(c.gen)
		if err != nil {
			t.Fatalf("%s: marshal the generated message: %v", c.name, err)
		}
		dm := newDynamicMessage(md)
		if err := proto.Unmarshal(want, dm); err != nil {
			t.Fatalf("%s: unmarshal into the synthesized descriptor: %v", c.name, err)
		}
		got, err := proto.Marshal(dm)
		if err != nil {
			t.Fatalf("%s: marshal the dynamic message: %v", c.name, err)
		}
		if string(want) != string(got) {
			t.Errorf("%s: the wire bytes mismatch\n generated: %x\n dynamic:   %x", c.name, want, got)
		}
	}
}
