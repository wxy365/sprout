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
	dm := newDynamicMessage(msgs["demo.descHelloReq"])
	if err := toMessage(reflect.ValueOf(in).Elem(), dm); err != nil {
		t.Fatalf("toMessage: %v", err)
	}
	var out descHelloReq
	if err := fromMessage(dm, reflect.ValueOf(&out).Elem()); err != nil {
		t.Fatalf("fromMessage: %v", err)
	}
	if out.Name != in.Name || out.Age != in.Age {
		t.Errorf("the scalars are not preserved: %+v", out)
	}
	if len(out.Tags) != 2 || out.Tags[0] != "a" {
		t.Errorf("the repeated field is not preserved: %v", out.Tags)
	}
	if out.Addr == nil || out.Addr.City != "Shanghai" {
		t.Errorf("the nested message is not preserved: %+v", out.Addr)
	}
	if out.Meta["k"] != "v" {
		t.Errorf("the map is not preserved: %v", out.Meta)
	}
	if string(out.Payload) != "bin" {
		t.Errorf("the bytes are not preserved: %v", out.Payload)
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
	dm := newDynamicMessage(msgs["demo.withTime"])
	if err := toMessage(reflect.ValueOf(&withTime{At: at}).Elem(), dm); err != nil {
		t.Fatalf("toMessage: %v", err)
	}
	var out withTime
	if err := fromMessage(dm, reflect.ValueOf(&out).Elem()); err != nil {
		t.Fatalf("fromMessage: %v", err)
	}
	if !out.At.Equal(at) {
		t.Errorf("the timestamp is not preserved: %v, want %v", out.At, at)
	}
}

func TestFromMessageSkipsTheAbsentFields(t *testing.T) {
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

func TestToMessageWithAPointerMessage(t *testing.T) {
	in := &descHelloReq{Addr: &descAddress{City: "Shanghai"}}
	_, msgs, err := buildFileDescriptor("demo", "demo/hello.proto", []reflect.Type{reflect.TypeOf(descHelloReq{})})
	if err != nil {
		t.Fatalf("buildFileDescriptor: %v", err)
	}
	dm := newDynamicMessage(msgs["demo.descHelloReq"])
	if err := toMessage(reflect.ValueOf(in).Elem(), dm); err != nil {
		t.Fatalf("toMessage: %v", err)
	}
	var out descHelloReq
	if err := fromMessage(dm, reflect.ValueOf(&out).Elem()); err != nil {
		t.Fatalf("fromMessage: %v", err)
	}
	if out.Addr == nil || out.Addr.Zip != "" {
		t.Errorf("the nested message is not preserved: %+v", out.Addr)
	}
}
