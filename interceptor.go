package svr

import "net/http"

type Interceptor func(next func(w http.ResponseWriter, r *http.Request) error) func(w http.ResponseWriter, r *http.Request) error

type InterceptorDef interface {
	Named
	Interceptor() Interceptor
}
