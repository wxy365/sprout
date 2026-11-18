package sprout

import (
	"context"
	"net/http"
	"time"

	"github.com/wxy365/basal/tp"
)

// requestContext wraps an HTTP request and response writer, providing convenience
// methods that delegate to the underlying request context. It implements
// a subset of the context.Context interface.
//
// The exported alias Context is declared in api.go, which collects the public
// API surface of the package.
type requestContext struct {
	// Request is the incoming HTTP request.
	Request *http.Request
	// Writer is the response writer used to send the HTTP response.
	Writer http.ResponseWriter
	// values carries the values that the framework attaches to the request.
	// It is empty when the context is built outside of the dispatcher.
	values ctxValues
}

// Deadline returns the deadline of the underlying request context.
func (c *requestContext) Deadline() (deadline time.Time, ok bool) {
	return c.Request.Context().Deadline()
}

// Done returns the done channel of the underlying request context.
func (c *requestContext) Done() <-chan struct{} {
	return c.Request.Context().Done()
}

// Err returns the error of the underlying request context, if any.
func (c *requestContext) Err() error {
	return c.Request.Context().Err()
}

// Value returns the value associated with key from the underlying request context.
func (c *requestContext) Value(key any) any {
	if c.values.parent != nil {
		if v, ok := c.values.lookup(key); ok {
			return v
		}
		// The values of the framework are looked up in the context that the
		// request carried before the dispatcher attached its own one, so that
		// the values of the middlewares stay reachable.
		return c.values.parent.Value(key)
	}
	return c.Request.Context().Value(key)
}

// ClientIP returns the client IP address by inspecting the request headers
// (e.g. X-Forwarded-For, X-Real-IP) and falling back to the remote address.
func (c *requestContext) ClientIP() string {
	return tp.GetClientIp(c.Request)
}

// setEndpointError records the error that the endpoint returned, so that the
// interceptors can tell whether the request was successful.
func (c *requestContext) setEndpointError(err error) {
	if c.values.parent != nil {
		c.values.endpointErr = err
		return
	}
	// The context was built outside of the dispatcher: the error is attached
	// to the context of the request instead. The context of the request is
	// the parent of the new one, so that the lookup of a value does not come
	// back to this context.
	newRequest := c.Request.WithContext(context.WithValue(c.Request.Context(), ctxKeyEndpointError, err))
	*c.Request = *newRequest
}

// setSSEStream records the stream of the SSE endpoint that is serving the
// request, so that the error handler can tell whether the response has already
// been committed.
func (c *requestContext) setSSEStream(stream *sseStream) {
	c.values.sseStream = stream
}

// sseStreamOf returns the stream of the SSE endpoint that is serving the
// request, or nil when the request is not served by an SSE endpoint.
func (c *requestContext) sseStreamOf() *sseStream {
	return c.values.sseStream
}

// ctxValues carries the values that the framework attaches to every request.
//
// The dispatcher used to attach them one by one, with a context.WithValue call
// per value. Every one of those calls allocated a value context and a copy of
// the request, so a single context that carries all the values replaces the
// whole chain.
type ctxValues struct {
	// parent is the context that the request carried before the framework
	// attached its own values. It is the fallback for the keys that the
	// framework does not know, like the locale of the request.
	parent context.Context
	// serializer writes the response of the endpoint.
	serializer Serializer
	// acceptType is the media type that the client accepts.
	acceptType string
	// deserializer reads the body of the request.
	deserializer Deserializer
	// contentTypeParams are the parameters of the content type of the request.
	contentTypeParams map[string]string
	// pathParams are the path parameters of the matched route.
	pathParams map[string]string
	// endpointErr is the error that the endpoint returned.
	endpointErr error
	// sseStream is the stream of the SSE endpoint that is serving the request.
	// It is nil for the requests that are not served by an SSE endpoint.
	sseStream *sseStream
}

// Deadline returns the deadline of the context that the request carried.
func (c *ctxValues) Deadline() (deadline time.Time, ok bool) {
	return c.parent.Deadline()
}

// Done returns the done channel of the context that the request carried.
func (c *ctxValues) Done() <-chan struct{} {
	return c.parent.Done()
}

// Err returns the error of the context that the request carried, if any.
func (c *ctxValues) Err() error {
	return c.parent.Err()
}

// Value returns the value associated with key. The keys that the framework
// does not know are looked up in the context that the request carried.
func (c *ctxValues) Value(key any) any {
	if v, ok := c.lookup(key); ok {
		return v
	}
	return c.parent.Value(key)
}

// lookup returns the value that the framework carries for the given key. It
// reports whether the key is one of them.
func (c *ctxValues) lookup(key any) (any, bool) {
	switch key.(type) {
	case ctxKeyTypeSerializer:
		return c.serializer, true
	case ctxKeyTypeAcceptType:
		return c.acceptType, true
	case ctxKeyTypeDeserializer:
		// The deserializer may be nil, when the media type of the request is
		// not supported. It is still returned, so that the callers can make
		// the type assertion they do today.
		return c.deserializer, true
	case ctxKeyTypeContentTypeParams:
		if c.contentTypeParams != nil {
			return c.contentTypeParams, true
		}
	case ctxKeyTypePathParams:
		if c.pathParams != nil {
			return c.pathParams, true
		}
	case ctxKeyTypeEndpointError:
		if c.endpointErr != nil {
			return c.endpointErr, true
		}
	}
	return nil, false
}

type ctxKeyTypePathParams struct{}

var ctxKeyPathParams ctxKeyTypePathParams

type ctxKeyTypeSerializer struct{}

var ctxKeySerializer ctxKeyTypeSerializer

type ctxKeyTypeAcceptType struct{}

var ctxKeyAcceptType ctxKeyTypeAcceptType

type ctxKeyTypeDeserializer struct{}

var ctxKeyDeserializer ctxKeyTypeDeserializer

// Content-Type header 附带的其他参数，比如multipart/form-data;boundary=-----WebKitFormBoundary7MA4YWxkTrZu0gW
// 附带的参数名为boundary，值为-----WebKitFormBoundary7MA4YWxkTrZu0gW
type ctxKeyTypeContentTypeParams struct{}

var ctxKeyContentTypeParams ctxKeyTypeContentTypeParams

type ctxKeyTypeEndpointError struct{}

var ctxKeyEndpointError ctxKeyTypeEndpointError
