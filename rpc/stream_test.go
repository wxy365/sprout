package rpc

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/wxy365/basal/errs"
)

// streamChatReq 是演示流方法的请求。
type streamChatReq struct {
	Text string `pb:"1"`
}

// streamChatResp 是演示流方法的响应。
type streamChatResp struct {
	Text string `pb:"1"`
}

// chatService 返回一个演示双向流方法的服务。
func chatService() *Service {
	return &Service{
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
	}
}

func TestBidirectionalStream(t *testing.T) {
	addr := startTestServer(t, chatService())

	client, err := NewClient("demo.Chat", WithDirect(addr))
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
			t.Errorf("the text = %q, want %q", resp.Text, "echo "+text)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("close send: %v", err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Errorf("the stream should end with io.EOF, got %v", err)
	}
}

func TestStreamReportsTheServerError(t *testing.T) {
	svc := &Service{
		Name: "demo.Chat",
		Methods: []Method{
			Stream("Chat", func(ctx *Context, s *ServerStream[streamChatReq, streamChatResp]) error {
				return errs.New("the chat is closed").WithCode("chat.closed").WithStatus(503)
			}),
		},
	}
	addr := startTestServer(t, svc)

	client, err := NewClient("demo.Chat", WithDirect(addr))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	chat := StreamDoer[streamChatReq, streamChatResp](client, "Chat")
	stream, err := chat(context.Background())
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	if err := stream.Send(&streamChatReq{Text: "a"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatalf("the server error should be reported to the caller")
	}
	var e *errs.Err
	if !errors.As(err, &e) {
		t.Fatalf("the error is not an *errs.Err: %v", err)
	}
	if e.Code != "chat.closed" {
		t.Errorf("the code = %q", e.Code)
	}
}

func TestStreamStopsWhenTheClientDisconnects(t *testing.T) {
	done := make(chan error, 1)
	svc := &Service{
		Name: "demo.Chat",
		Methods: []Method{
			Stream("Chat", func(ctx *Context, s *ServerStream[streamChatReq, streamChatResp]) error {
				for {
					if _, err := s.Recv(); err != nil {
						done <- err
						return nil
					}
				}
			}),
		},
	}
	addr := startTestServer(t, svc)

	client, err := NewClient("demo.Chat", WithDirect(addr))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	chat := StreamDoer[streamChatReq, streamChatResp](client, "Chat")
	stream, err := chat(context.Background())
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	if err := stream.Send(&streamChatReq{Text: "a"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	client.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Errorf("the server should see the disconnect")
		}
	case <-time.After(5 * time.Second):
		t.Errorf("the server did not notice the disconnect")
	}
}
