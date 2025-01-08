package sprout

import "context"

type Handler func(ctx context.Context, in any) (any, error)

type Endpoint struct {
	Name         string
	Pattern      string
	Methods      []string
	Handler      Handler
	InputEntity  any
	Interceptors []Interceptor
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
	return func(ctx context.Context, in any) (any, error) {
		return nil, nil
	}
}

func (b *BaseEndpoint) InputEntity() any {
	return nil
}

func (b *BaseEndpoint) OutputEntity() any {
	return nil
}

func (b *BaseEndpoint) Interceptors() []Interceptor {
	return []Interceptor{}
}
