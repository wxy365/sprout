package sprout

import (
	"net/http"

	"github.com/wxy365/basal/errs"
)

var (
	// errRateLimited is returned when a request is rejected due to rate limiting.
	errRateLimited = errs.New("Too many request").WithCode("RATE_LIMITED").WithStatus(http.StatusTooManyRequests)
	// errCircuitBroken is returned when a request is rejected because the circuit
	// breaker is open and the endpoint is not accepting requests.
	errCircuitBroken = errs.New("The request was blocked").WithCode("CIRCUIT_BROKEN").WithStatus(http.StatusInternalServerError)
)
