package sprout

import (
	"net/http"

	"github.com/wxy365/basal/errs"
)

var (
	// ErrRateLimited is returned when a request is rejected due to rate limiting.
	ErrRateLimited = errs.New("Too many request").WithCode("RATE_LIMITED").WithStatus(http.StatusTooManyRequests)
	// ErrCircuitBroken is returned when a request is rejected because the circuit
	// breaker is open and the endpoint is not accepting requests.
	ErrCircuitBroken = errs.New("The request was blocked").WithCode("CIRCUIT_BROKEN").WithStatus(http.StatusInternalServerError)
)
