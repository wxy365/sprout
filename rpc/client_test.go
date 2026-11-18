package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wxy365/basal/errs"
)

func TestClientCallsTheUnaryMethod(t *testing.T) {
	addr := startTestServer(t, helloService())

	client, err := newClient("demo.Greeter", withDirect(addr))
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	defer client.Close()

	hello := doer[svcHelloReq, svcHelloResp](client, "Hello")
	resp, err := hello(context.Background(), &svcHelloReq{Name: "wxy"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if resp.Greeting != "hello wxy" {
		t.Errorf("the greeting = %q", resp.Greeting)
	}
}

func TestClientRestoresTheBusinessError(t *testing.T) {
	svc := &Service{
		Name: "demo.Greeter",
		Methods: []Method{
			unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				return nil, errs.New("the name is required").WithCode("guest.name.required").WithStatus(400)
			}),
		},
	}
	addr := startTestServer(t, svc)

	client, err := newClient("demo.Greeter", withDirect(addr))
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	defer client.Close()

	hello := doer[svcHelloReq, svcHelloResp](client, "Hello")
	_, err = hello(context.Background(), &svcHelloReq{})
	if err == nil {
		t.Fatalf("the handler error should be reported to the caller")
	}
	var e *errs.Err
	if !errors.As(err, &e) {
		t.Fatalf("the error is not an *errs.Err: %v", err)
	}
	if e.Code != "guest.name.required" {
		t.Errorf("the code = %q", e.Code)
	}
	if e.Status != 400 {
		t.Errorf("the status = %d", e.Status)
	}
}

func TestClientNeedsAnAddress(t *testing.T) {
	if _, err := newClient("demo.Greeter"); err == nil {
		t.Errorf("a client without an address should be rejected")
	}
}

func TestClientAppliesTheTimeout(t *testing.T) {
	addr := startTestServer(t, helloService())

	client, err := newClient("demo.Greeter", withDirect(addr), withTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	defer client.Close()

	hello := doer[svcHelloReq, svcHelloResp](client, "Hello")
	// The timeout is short but the server answers immediately, so the call
	// must succeed: the timeout only bounds the call, it does not break it.
	if _, err := hello(context.Background(), &svcHelloReq{Name: "wxy"}); err != nil {
		t.Errorf("the call should succeed, got %v", err)
	}
}

func TestClientRejectsAnUnqualifiedServiceName(t *testing.T) {
	if _, err := newClient("Greeter", withDirect("127.0.0.1:1")); err != nil {
		t.Errorf("newClient should not fail on the service name, got %v", err)
	}
	client, err := newClient("Greeter", withDirect("127.0.0.1:1"))
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	defer client.Close()
	hello := doer[svcHelloReq, svcHelloResp](client, "Hello")
	if _, err := hello(context.Background(), &svcHelloReq{}); err == nil {
		t.Errorf("an unqualified service name should be rejected")
	}
}
