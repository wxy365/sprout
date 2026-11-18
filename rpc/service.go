package rpc

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	sp "github.com/wxy365/sprout"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Context 是 gRPC 调用的上下文。
//
// The exported alias Context is declared in api.go, which collects the public
// API surface of the package.
type Context struct {
	context.Context
	// md 是调用方传入的元数据。
	md metadata.MD
}

// MD 返回调用方传入的元数据。
func (c *Context) MD() metadata.MD {
	return c.md
}

// methodKind 是方法的通信模型。
type methodKind int

const (
	// kindUnary 是一元调用。
	kindUnary methodKind = iota
	// kindStream 是双向流调用。
	kindStream
)

// Method 描述一个 gRPC 方法。由 Unary 与 Stream 构造，不直接实例化。
//
// The exported alias Method is declared in api.go, which collects the public
// API surface of the package.
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

// Service 描述一个 gRPC 服务及其方法。
//
// The exported alias Service is declared in api.go, which collects the public
// API surface of the package.
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
//
// The exported wrapper Unary is declared in api.go, which collects the public
// API surface of the package.
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
			callCtx := &Context{Context: ctx, md: metadataFromIncoming(ctx)}
			res, err := m.handler(callCtx, in.Interface().(*I))
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

// serviceFileName 返回服务对应的 protobuf 文件名。
func serviceFileName(serviceName string) string {
	_, name, err := servicePackage(serviceName)
	if err != nil {
		return "service"
	}
	return strings.ToLower(name)
}

// buildServiceDesc 把服务定义拼装成 grpc-go 的服务描述符。
func (s *Service) buildServiceDesc() (*grpc.ServiceDesc, error) {
	pkg, _, err := servicePackage(s.Name)
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
		_, msgs, err := buildFileDescriptor(pkg, pkg+"/"+serviceFileName(s.Name)+".proto", []reflect.Type{inType, outType})
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
