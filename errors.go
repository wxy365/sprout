package sprout

import (
	"net/http"

	"github.com/wxy365/basal/errs"
)

var (
	ErrRateLimited   = errs.New("Too many request").WithCode("RATE_LIMITED").WithStatus(http.StatusTooManyRequests)
	ErrCircuitBroken = errs.New("The request was blocked").WithCode("CIRCUIT_BROKEN").WithStatus(http.StatusInternalServerError)
)
