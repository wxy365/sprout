package sprout

import (
	"github.com/wxy365/basal/tp"
	"net/http"
	"time"
)

type Context struct {
	Request *http.Request
	Writer  http.ResponseWriter
}

func (c *Context) Deadline() (deadline time.Time, ok bool) {
	return c.Request.Context().Deadline()
}

func (c *Context) Done() <-chan struct{} {
	return c.Request.Context().Done()
}

func (c *Context) Err() error {
	return c.Request.Context().Err()
}

func (c *Context) Value(key any) any {
	return c.Request.Context().Value(key)
}

func (c *Context) ClientIP() string {
	return tp.GetClientIp(c.Request)
}

type ctxKeyTypePathParams uint8

var ctxKeyPathParams ctxKeyTypePathParams

type ctxKeyTypeSerializer uint8

var ctxKeySerializer ctxKeyTypeSerializer

type ctxKeyTypeAcceptType uint8

var ctxKeyAcceptType ctxKeyTypeAcceptType

type ctxKeyTypeDeserializer uint8

var ctxKeyDeserializer ctxKeyTypeDeserializer

// Content-Type header 附带的其他参数，比如multipart/form-data;boundary=-----WebKitFormBoundary7MA4YWxkTrZu0gW
// 附带的参数名为boundary，值为-----WebKitFormBoundary7MA4YWxkTrZu0gW
type ctxKeyTypeContentTypeParams uint8

var ctxKeyContentTypeParams ctxKeyTypeContentTypeParams

type ctxKeyTypeEndpointError uint8

var ctxKeyEndpointError ctxKeyTypeEndpointError
