package rpc

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/wxy365/basal/errs"
	sp "github.com/wxy365/sprout"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// startTestServer 启动一个只监听本机的 gRPC 服务端，返回监听地址。
func startTestServer(t *testing.T, svc *Service) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &Server{Name: "test", ShutdownTimeout: 2 * time.Second}
	srv.addService(svc)
	go srv.serve(lis)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Stop(ctx)
	})
	return lis.Addr().String()
}

// helloService 返回一个演示一元方法的服务。
func helloService() *Service {
	return &Service{
		Name: "demo.Greeter",
		Methods: []Method{
			Unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				return &svcHelloResp{Greeting: "hello " + in.Name}, nil
			}),
		},
	}
}

func TestServerServesTheMountedService(t *testing.T) {
	addr := startTestServer(t, helloService())

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	_, msgs, err := buildFileDescriptor("demo", "demo/greeter.proto", []reflect.Type{reflect.TypeOf(svcHelloReq{}), reflect.TypeOf(svcHelloResp{})})
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
		t.Errorf("the greeting = %q", resp.Greeting)
	}
}

func TestServerReportsTheHandlerError(t *testing.T) {
	svc := &Service{
		Name: "demo.Greeter",
		Methods: []Method{
			Unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				return nil, errs.New("the name is required").WithCode("guest.name.required").WithStatus(400)
			}),
		},
	}
	addr := startTestServer(t, svc)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	_, msgs, err := buildFileDescriptor("demo", "demo/greeter.proto", []reflect.Type{reflect.TypeOf(svcHelloReq{}), reflect.TypeOf(svcHelloResp{})})
	if err != nil {
		t.Fatalf("buildFileDescriptor: %v", err)
	}
	in := newDynamicMessage(msgs["demo.svcHelloReq"])
	out := newDynamicMessage(msgs["demo.svcHelloResp"])
	err = conn.Invoke(context.Background(), "/demo.Greeter/Hello", in, out)
	if err == nil {
		t.Fatalf("the handler error should be reported to the caller")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("the error is not a gRPC status: %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("the code = %s, want %s", st.Code(), codes.InvalidArgument)
	}
	if st.Message() != "the name is required" {
		t.Errorf("the message = %q", st.Message())
	}
}

func TestServerValidatesTheInput(t *testing.T) {
	type validatedReq struct {
		Name string `pb:"1" validate:"required"`
	}
	type validatedResp struct {
		Ok bool `pb:"1"`
	}
	svc := &Service{
		Name: "demo.Validated",
		Methods: []Method{
			Unary("Hello", func(ctx *Context, in *validatedReq) (*validatedResp, error) {
				return &validatedResp{Ok: true}, nil
			}),
		},
	}
	addr := startTestServer(t, svc)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	_, msgs, err := buildFileDescriptor("demo", "demo/validated.proto", []reflect.Type{reflect.TypeOf(validatedReq{}), reflect.TypeOf(validatedResp{})})
	if err != nil {
		t.Fatalf("buildFileDescriptor: %v", err)
	}
	in := newDynamicMessage(msgs["demo.validatedReq"])
	out := newDynamicMessage(msgs["demo.validatedResp"])
	err = conn.Invoke(context.Background(), "/demo.Validated/Hello", in, out)
	if err == nil {
		t.Fatalf("the invalid input should be rejected")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("the code = %s, want %s", st.Code(), codes.InvalidArgument)
	}
}

func TestMountFindsTheServerByName(t *testing.T) {
	app := &sp.App[struct{}]{}
	srv := &Server{Name: "grpc"}
	app.Runners = append(app.Runners, srv)

	Mount(helloService(), app, "grpc")
	if len(app.Initializers) != 1 {
		t.Fatalf("the initializers = %d, want 1", len(app.Initializers))
	}
	if err := app.Initializers[0](app); err != nil {
		t.Fatalf("the initializer: %v", err)
	}
	if len(srv.services) != 1 {
		t.Errorf("the mounted services = %d, want 1", len(srv.services))
	}
}

func TestMountReportsTheMissingServer(t *testing.T) {
	app := &sp.App[struct{}]{}
	Mount(helloService(), app, "missing")
	err := app.Initializers[0](app)
	if err == nil {
		t.Fatalf("mounting to a missing server should fail")
	}
}
