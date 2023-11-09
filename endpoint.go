package svr

import "context"

type Handler func(ctx context.Context, in any, out any) error

type Endpoint interface {
	Named
	Pattern() string
	Methods() []string
	Handler() Handler
	InputEntity() any
	OutputEntity() any
	Interceptors() []string
}

type BaseEndpoint struct {
}

func (b *BaseEndpoint) Name() string {
	panic("The endpoint name cannot be empty")
}

func (b *BaseEndpoint) Pattern() string {
	panic("The endpoint pattern cannot be empty")
}

func (b *BaseEndpoint) Methods() []string {
	panic("The endpoint methods cannot be empty")
}

func (b *BaseEndpoint) Handler() Handler {
	return func(ctx context.Context, in any, out any) error {
		return nil
	}
}

func (b *BaseEndpoint) InputEntity() any {
	return nil
}

func (b *BaseEndpoint) OutputEntity() any {
	return nil
}

func (b *BaseEndpoint) Interceptors() []string {
	return []string{""}
}
