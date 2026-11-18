package sprout

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wxy365/basal/errs"
)

// TestContextLookupAfterEndpointError verifies that an interceptor that reads a
// context value after the handler failed does not deadlock.
//
// The endpoint error is recorded in the values of the request. The chain it
// is stored in must not be rooted at the request context itself, because the
// request context resolves its values by delegating to the very request it
// holds.
func TestContextLookupAfterEndpointError(t *testing.T) {
	looked := make(chan any, 1)
	e := &Endpoint[endpointInput, *endpointOutput]{
		Name:    "greet",
		Pattern: "/greet",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *Context, in endpointInput) (*endpointOutput, error) {
			return nil, errs.New("boom").WithStatus(http.StatusConflict)
		},
		Interceptors: []Interceptor{
			func(next func(ctx *requestContext) error) func(ctx *requestContext) error {
				return func(ctx *requestContext) error {
					err := next(ctx)
					// The interceptor reads a context value that is not the
					// endpoint error, after the handler has failed.
					go func() {
						looked <- ctx.Value(ctxKeyAcceptType)
					}()
					select {
					case v := <-looked:
						if v != MimeJson {
							t.Errorf("the accept type = %v, want %v", v, MimeJson)
						}
					case <-time.After(2 * time.Second):
						t.Error("looking up a context value after the endpoint error deadlocked")
					}
					return err
				}
			},
		},
	}

	rec := serveEndpoint(t, e, httptest.NewRequest(http.MethodGet, "/greet?name=foo", nil))
	if rec.Code != http.StatusConflict {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusConflict)
	}
}
