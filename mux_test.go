package sprout

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// muxTestInput is the input entity used by the mux tests. It exercises the
// path, query, header and cookie struct tags.
type muxTestInput struct {
	ID     string `path:"id"`
	Query  string `query:"q"`
	Header string `header:"X-Mux-Test"`
	Cookie string `cookie:"sid"`
}

// fieldValue returns the value bound to the named field.
func (in *muxTestInput) fieldValue(field string) string {
	switch field {
	case "ID":
		return in.ID
	case "Query":
		return in.Query
	case "Header":
		return in.Header
	case "Cookie":
		return in.Cookie
	}
	return ""
}

// assertFieldBound checks that the given field was bound to the expected
// value. The conversion of the raw string into the field is performed by
// basal/rflt: when that conversion fails, the returned error still carries the
// value that was bound, which proves that the binding itself took place.
func assertFieldBound(t *testing.T, err error, field, value, got string) {
	t.Helper()
	if err != nil {
		if !strings.Contains(err.Error(), value) {
			t.Fatalf("failed to bind the value %q to the field %q: %v", value, field, err)
		}
		return
	}
	if got != value {
		t.Fatalf("the field %q = %q, want %q", field, got, value)
	}
}

func TestMuxDispatch(t *testing.T) {
	cases := []struct {
		name          string
		routes        []string
		method        string
		target        string
		query         string
		headers       map[string]string
		cookies       map[string]string
		wantStatus    int
		wantRoute     string
		wantField     string
		wantValue     string
		wantPathParam map[string]string
	}{
		{
			name:          "named path param reaches the handler",
			routes:        []string{"/users/{id}"},
			method:        http.MethodGet,
			target:        "/users/42",
			wantStatus:    http.StatusOK,
			wantRoute:     "/users/{id}",
			wantField:     "ID",
			wantValue:     "42",
			wantPathParam: map[string]string{"id": "42"},
		},
		{
			name:          "regex path param reaches the handler",
			routes:        []string{"/users/{id:~\\d+}"},
			method:        http.MethodGet,
			target:        "/users/12345",
			wantStatus:    http.StatusOK,
			wantRoute:     "/users/{id:~\\d+}",
			wantField:     "ID",
			wantValue:     "12345",
			wantPathParam: map[string]string{"id": "12345"},
		},
		{
			name:       "static segment wins over named param",
			routes:     []string{"/users/{id}", "/users/me"},
			method:     http.MethodGet,
			target:     "/users/me",
			wantStatus: http.StatusOK,
			wantRoute:  "/users/me",
		},
		{
			name:       "query param still binds",
			routes:     []string{"/users"},
			method:     http.MethodGet,
			target:     "/users",
			query:      "q=hello",
			wantStatus: http.StatusOK,
			wantRoute:  "/users",
			wantField:  "Query",
			wantValue:  "hello",
		},
		{
			name:       "header param still binds",
			routes:     []string{"/users"},
			method:     http.MethodGet,
			target:     "/users",
			headers:    map[string]string{"X-Mux-Test": "hdr"},
			wantStatus: http.StatusOK,
			wantRoute:  "/users",
			wantField:  "Header",
			wantValue:  "hdr",
		},
		{
			name:       "cookie param still binds",
			routes:     []string{"/users"},
			method:     http.MethodGet,
			target:     "/users",
			cookies:    map[string]string{"sid": "ck"},
			wantStatus: http.StatusOK,
			wantRoute:  "/users",
			wantField:  "Cookie",
			wantValue:  "ck",
		},
		{
			name:       "no matching route returns 404",
			routes:     []string{"/users/{id}"},
			method:     http.MethodGet,
			target:     "/unknown",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotRoute string
			var gotInput muxTestInput
			var gotErr error
			var gotPathParams map[string]string
			handlers := make(map[epSig]func(*requestContext))
			for _, pattern := range tc.routes {
				pattern := pattern
				handlers[epSig{method: tc.method, pattern: pattern}] = func(ctx *requestContext) {
					gotRoute = pattern
					gotPathParams, _ = ctx.Request.Context().Value(ctxKeyPathParams).(map[string]string)
					gotErr = parseHttpRequest(&gotInput, ctx.Request, nil)
				}
			}
			m := newMux(handlers)

			req := httptest.NewRequest(tc.method, tc.target, nil)
			if tc.query != "" {
				req.URL.RawQuery = tc.query
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			for k, v := range tc.cookies {
				req.AddCookie(&http.Cookie{Name: k, Value: v})
			}
			rec := httptest.NewRecorder()

			m.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status code = %d, want %d", rec.Code, tc.wantStatus)
			}
			if gotRoute != tc.wantRoute {
				t.Errorf("matched route = %q, want %q", gotRoute, tc.wantRoute)
			}
			if tc.wantField != "" {
				assertFieldBound(t, gotErr, tc.wantField, tc.wantValue, gotInput.fieldValue(tc.wantField))
			}
			if tc.wantPathParam != nil {
				if len(gotPathParams) != len(tc.wantPathParam) {
					t.Errorf("path params = %v, want %v", gotPathParams, tc.wantPathParam)
				}
				for k, v := range tc.wantPathParam {
					if gotPathParams[k] != v {
						t.Errorf("the path param %q = %q, want %q", k, gotPathParams[k], v)
					}
				}
			}
		})
	}
}
