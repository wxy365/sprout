package sprout

type ctxKeyTypePathParams uint8

const ctxKeyPathParams ctxKeyTypePathParams = 0

type ctxKeyTypeSerializer uint8

const ctxKeySerializer ctxKeyTypeSerializer = 0

type ctxKeyTypeAcceptType uint8

const ctxKeyAcceptType ctxKeyTypeAcceptType = 0

type ctxKeyTypeDeserializer uint8

const ctxKeyDeserializer ctxKeyTypeDeserializer = 0

// Content-Type header 附带的其他参数，比如multipart/form-data;boundary=-----WebKitFormBoundary7MA4YWxkTrZu0gW
// 附带的参数名为boundary，值为-----WebKitFormBoundary7MA4YWxkTrZu0gW
type ctxKeyTypeContentTypeParams uint8

const ctxKeyContentTypeParams ctxKeyTypeContentTypeParams = 0

type ctxKeyTypeEndpointError uint8

const ctxKeyEndpointError ctxKeyTypeEndpointError = 0
