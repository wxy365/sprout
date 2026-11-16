package sprout

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/wxy365/basal/errs"
)

// errStatus extracts the HTTP status code carried by an error. It returns
// 0 when the error does not carry one.
func errStatus(err error) int {
	var e *errs.Err
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// newResponseRecorder builds a response recorder for the tests.
func newResponseRecorder() *httptest.ResponseRecorder {
	return httptest.NewRecorder()
}

// newTestRequest builds a request whose context carries the serializer and the
// deserializer that the framework injects while serving a request. Both values
// are required by the request parsing and the response serializing.
func newTestRequest(method, target, body string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	ctx := req.Context()
	ctx = context.WithValue(ctx, ctxKeySerializer, Serializer(SerializeJson))
	ctx = context.WithValue(ctx, ctxKeyDeserializer, Deserializer(DeserializeJson))
	ctx = context.WithValue(ctx, ctxKeyAcceptType, MimeJson)
	return req.WithContext(ctx)
}
