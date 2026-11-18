package rpc

import (
	"context"
	"io"
	"reflect"

	sp "github.com/wxy365/sprout"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ServerStream 是服务端的双向流句柄。业务逻辑通过它收发消息。
//
// The exported alias ServerStream is declared in api.go, which collects the
// public API surface of the package.
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
//
// The exported alias ClientStream is declared in api.go, which collects the
// public API surface of the package.
type ClientStream[I any, O any] struct {
	// stream 是底层的 grpc 流。
	stream grpc.ClientStream
	// inDesc 是入参的消息描述符。
	inDesc protoreflect.MessageDescriptor
	// outDesc 是出参的消息描述符。
	outDesc protoreflect.MessageDescriptor
	// cancel 释放调用上下文持有的资源。
	cancel context.CancelFunc
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
// 服务端返回的业务错误会被还原成 *errs.Err。
func (s *ClientStream[I, O]) Recv() (*O, error) {
	outMsg := newDynamicMessage(s.outDesc)
	if err := s.stream.RecvMsg(outMsg); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fromStatus(err)
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

// Stream 构造一个双向流方法。I 是请求类型，O 是响应类型。
//
// The exported wrapper Stream is declared in api.go, which collects the
// public API surface of the package.
func Stream[I, O any](name string, handler func(ctx *Context, stream *ServerStream[I, O]) error) Method {
	return &streamMethod[I, O]{name: name, handler: handler}
}

func (m *streamMethod[I, O]) methodName() string { return m.name }

func (m *streamMethod[I, O]) methodKind() methodKind { return kindStream }

func (m *streamMethod[I, O]) goTypes() (reflect.Type, reflect.Type) {
	return reflect.TypeOf((*I)(nil)).Elem(), reflect.TypeOf((*O)(nil)).Elem()
}

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
