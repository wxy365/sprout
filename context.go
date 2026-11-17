package sprout

import (
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
	return c.Request.Context().Value(key)
}

// ClientIP returns the client IP address by inspecting the request headers
// (e.g. X-Forwarded-For, X-Real-IP) and falling back to the remote address.
func (c *requestContext) ClientIP() string {
	return tp.GetClientIp(c.Request)
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
