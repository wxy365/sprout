package sprout

import (
	"github.com/wxy365/basal/lei"
	"net/http"
)

var (
	ErrRateLimited   = lei.New("Too many request").WithCode("RATE_LIMITED").WithStatus(http.StatusTooManyRequests)
	ErrCircuitBroken = lei.New("The request was blocked").WithCode("CIRCUIT_BROKEN").WithStatus(http.StatusInternalServerError)
)
