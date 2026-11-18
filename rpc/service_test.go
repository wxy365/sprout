package rpc

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

// svcHelloReq 是演示一元方法的消息。
type svcHelloReq struct {
	Name string `pb:"1"`
}

// svcHelloResp 是演示一元方法的响应。
type svcHelloResp struct {
	Greeting string `pb:"1"`
}

func TestServiceBuildsTheServiceDesc(t *testing.T) {
	svc := &Service{
		Name: "demo.Greeter",
		Methods: []Method{
			unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				return &svcHelloResp{Greeting: "hello " + in.Name}, nil
			}),
		},
	}
	desc, err := svc.buildServiceDesc()
	if err != nil {
		t.Fatalf("buildServiceDesc: %v", err)
	}
	if desc.ServiceName != "demo.Greeter" {
		t.Errorf("the service name = %q", desc.ServiceName)
	}
	if len(desc.Methods) != 1 {
		t.Fatalf("the methods = %d, want 1", len(desc.Methods))
	}
	if desc.Methods[0].MethodName != "Hello" {
		t.Errorf("the method name = %q", desc.Methods[0].MethodName)
	}
	if len(desc.Streams) != 0 {
		t.Errorf("the streams = %d, want 0", len(desc.Streams))
	}
}

func TestServiceRejectsAnInvalidDefinition(t *testing.T) {
	cases := []struct {
		name string
		svc  *Service
	}{
		{"without a package", &Service{Name: "Greeter"}},
		{"without a method", &Service{Name: "demo.Greeter"}},
		{"with a duplicated method", &Service{
			Name: "demo.Greeter",
			Methods: []Method{
				unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
					return &svcHelloResp{}, nil
				}),
				unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
					return &svcHelloResp{}, nil
				}),
			},
		}},
		{"with an invalid message", &Service{
			Name: "demo.Greeter",
			Methods: []Method{
				unary("Hello", func(ctx *Context, in *struct {
					Name string
				}) (*svcHelloResp, error) {
					return &svcHelloResp{}, nil
				}),
			},
		}},
	}
	for _, c := range cases {
		if _, err := c.svc.buildServiceDesc(); err == nil {
			t.Errorf("%s: an error is expected, but got none", c.name)
		}
	}
}

func TestContextExposesTheIncomingMetadata(t *testing.T) {
	md := metadata.Pairs("x-client-id", "c1")
	ctx := &Context{Context: context.Background(), md: md}
	if got := ctx.MD().Get("x-client-id"); len(got) != 1 || got[0] != "c1" {
		t.Errorf("the metadata = %v", ctx.MD())
	}
}

func TestServicePackage(t *testing.T) {
	pkg, name, err := servicePackage("demo.Greeter")
	if err != nil {
		t.Fatalf("servicePackage: %v", err)
	}
	if pkg != "demo" || name != "Greeter" {
		t.Errorf("the package = %q, the name = %q", pkg, name)
	}
	if _, _, err := servicePackage("Greeter"); err == nil {
		t.Errorf("a service name without a package should be rejected")
	}
}
