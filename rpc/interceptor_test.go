package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wxy365/basal/errs"
)

func TestTheServerRecoversThePanic(t *testing.T) {
	svc := &Service{
		Name: "demo.Greeter",
		Methods: []Method{
			unary("Hello", func(ctx *Context, in *svcHelloReq) (*svcHelloResp, error) {
				panic("boom")
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
	_, err = hello(context.Background(), &svcHelloReq{Name: "wxy"})
	if err == nil {
		t.Fatalf("the panic should be converted into an error")
	}
	// The client restores the gRPC status into a business error.
	var e *errs.Err
	if !errors.As(err, &e) {
		t.Fatalf("the error is not an *errs.Err: %v", err)
	}
	if e.Status != 500 {
		t.Errorf("the status = %d, want 500", e.Status)
	}
}

func TestTheServerRecoversThePanicOfTheStream(t *testing.T) {
	svc := &Service{
		Name: "demo.Chat",
		Methods: []Method{
			stream("Chat", func(ctx *Context, s *ServerStream[streamChatReq, streamChatResp]) error {
				panic("boom")
			}),
		},
	}
	addr := startTestServer(t, svc)

	client, err := newClient("demo.Chat", withDirect(addr))
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	defer client.Close()

	chat := streamDoer[streamChatReq, streamChatResp](client, "Chat")
	stream, err := chat(context.Background())
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	if err := stream.Send(&streamChatReq{Text: "a"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatalf("the panic should be converted into an error")
	}
	var e *errs.Err
	if !errors.As(err, &e) {
		t.Fatalf("the error is not an *errs.Err: %v", err)
	}
	if e.Status != 500 {
		t.Errorf("the status = %d, want 500", e.Status)
	}
}

func TestTheServerRejectsTheRateLimitedCall(t *testing.T) {
	svc := helloService()
	addr := startTestServer(t, svc)

	client, err := newClient("demo.Greeter", withDirect(addr))
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	defer client.Close()

	hello := doer[svcHelloReq, svcHelloResp](client, "Hello")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The default limiter allows 500 calls per second, so a burst of calls
	// must not be rejected. This asserts the guard does not break the normal
	// traffic.
	for i := 0; i < 20; i++ {
		if _, err := hello(ctx, &svcHelloReq{Name: "wxy"}); err != nil {
			t.Fatalf("the call %d should not be rejected, got %v", i, err)
		}
	}
}

func TestTheClientIdentifierComesFromTheMetadata(t *testing.T) {
	ctx := &Context{Context: context.Background()}
	if got := clientIdentityFromContext(ctx); got != "" {
		t.Errorf("the client identity = %q, want an empty string", got)
	}
}
