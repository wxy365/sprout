package sprout

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/sony/gobreaker"
	"github.com/wxy365/basal/cfg/def"
	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
	"github.com/wxy365/basal/tp"
	"golang.org/x/time/rate"
)

// interceptorFunc is a middleware function that wraps an endpoint handler.
// Interceptors are chained so that each one can perform pre-processing,
// post-processing, or short-circuit the request before it reaches the handler.
//
// The exported alias Interceptor is declared in api.go, which collects the
// public API surface of the package.
type interceptorFunc func(next func(*requestContext) error) func(ctx *requestContext) error

// appendServerInterceptors wraps the given handler with the interceptors, the
// first one being the outermost.
func appendServerInterceptors(handler func(*requestContext) error, interceptors ...interceptorFunc) func(*requestContext) error {
	for i := len(interceptors) - 1; i >= 0; i-- {
		handler = interceptors[i](handler)
	}
	return handler
}

type circuitBreakerSettings struct {
	MaxRequests            uint32        `map:"max_requests"`
	Interval               time.Duration `map:"interval"`
	Timeout                time.Duration `map:"timeout"`
	MaxConsecutiveFailures uint32        `map:"max_consecutive_failures"`
	MaxFailureRatio        float64       `map:"failure_ratio"`
}

func getCircuitBreakerSettings(breakerName string) *circuitBreakerSettings {
	scbs, _ := def.GetObj[map[string]circuitBreakerSettings]("app.breakers")
	if len(scbs) == 0 {
		return nil
	}
	if cbs, ok := scbs[breakerName]; ok {
		return &cbs
	}
	return nil
}

func newCircuitBreaker(cbs *circuitBreakerSettings) *gobreaker.TwoStepCircuitBreaker {
	return gobreaker.NewTwoStepCircuitBreaker(gobreaker.Settings{
		Name:        "DEFAULT_SPROUT_CIRCUIT_BREAKER",
		MaxRequests: cbs.MaxRequests,
		Interval:    cbs.Interval,
		Timeout:     cbs.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			ratio := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.ConsecutiveFailures >= cbs.MaxConsecutiveFailures && ratio >= cbs.MaxFailureRatio
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			log.Info("Circuit breaker state changed, Name: [{0}], from: [{1}], to: [{2}]", name, from, to)
		},
	})
}

// newCircuitBreakerInterceptor creates a circuit breaker with the given Name.
// If the settings for the breaker is not configured, then the default settings is applied.
func newCircuitBreakerInterceptor(breakerName string) interceptorFunc {
	cbs := getCircuitBreakerSettings(breakerName)
	if cbs == nil {
		cbs = &circuitBreakerSettings{
			MaxRequests:            5,
			Interval:               15 * time.Second,
			Timeout:                15 * time.Second,
			MaxConsecutiveFailures: 10,
			MaxFailureRatio:        0.6,
		}
	}
	breaker := newCircuitBreaker(cbs)
	return func(next func(ctx *requestContext) error) func(ctx *requestContext) error {
		return func(ctx *requestContext) error {
			done, err := breaker.Allow()
			if err != nil {
				return errCircuitBroken
			}
			var panicked bool
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					done(false)
					panic(r)
				}
				if panicked {
					return
				}
				isSuccessful := true
				if er := ctx.Value(ctxKeyEndpointError); er != nil {
					err := er.(error)
					var e *errs.Err
					if errors.As(err, &e) {
						if e.Status >= http.StatusInternalServerError {
							isSuccessful = false
						}
					}
				}
				done(isSuccessful)
			}()
			return next(ctx)
		}
	}
}

var (
	// clientIdentifiers is a map of client identifier names to their corresponding
	// ClientIdentifier functions, used in client rate limiting.
	// Callers can register client identifiers through RegisterClientIdentifier.
	clientIdentifiers   = make(map[string]clientIdentifierFunc)
	clientIdentifiersMu sync.RWMutex
)

// clientIdentifierFunc is a function that extracts a client identity string from
// an HTTP request. It is used by the rate limiter to apply per-client limits.
// The default implementation returns the client IP address.
//
// The exported alias ClientIdentifier is declared in api.go, which collects the
// public API surface of the package.
type clientIdentifierFunc func(*http.Request) string

// registerClientIdentifier registers a named client identifier function that
// can be referenced by the rate limiter configuration.
//
// The exported wrapper RegisterClientIdentifier is declared in api.go, which
// collects the public API surface of the package.
func registerClientIdentifier(name string, identifier clientIdentifierFunc) {
	clientIdentifiersMu.Lock()
	clientIdentifiers[name] = identifier
	clientIdentifiersMu.Unlock()
}

// The rate limiter controls the traffic at both the client and the server interface simultaneously
//
// A limiter that is not configured is not applied to the client side at all. A
// limiter that is configured applies the client side limit only when a client
// token rate is set, because a limiter without a token rate rejects every
// request.
func newRateLimiterInterceptor(limiterName string) interceptorFunc {
	rls := getRateLimiterSettings(limiterName)
	var clientLimiters *cache.Cache
	defaultExpiration := time.Second
	if rls == nil {
		rls = &rateLimiterSettings{
			TokenRate:       500,
			TokenBucketSize: 500,
		}
	} else {
		if rls.TokenRate < 0 {
			rls.TokenRate = rate.Inf
		}
		if rls.ClientTokenRate < 0 {
			rls.ClientTokenRate = rate.Inf
		}
		// A token bucket of zero size never grants a token, so a bucket size
		// that is not specified falls back to the token rate.
		rls.TokenBucketSize = fallbackTokenBucketSize(rls.TokenRate, rls.TokenBucketSize)
		rls.ClientTokenBucketSize = fallbackTokenBucketSize(rls.ClientTokenRate, rls.ClientTokenBucketSize)
		if rls.ClientTokenRate > 0 {
			if rls.ClientTokenRate < 1 {
				defaultExpiration = time.Second * time.Duration(1/rls.ClientTokenRate)
			}
			clientLimiters = cache.New(defaultExpiration, time.Second)
		}
	}
	serverLimiter := rate.NewLimiter(rls.TokenRate, rls.TokenBucketSize)

	clientIdentifiersMu.RLock()
	clientIdentifier := clientIdentifiers[rls.ClientIdentifierType]
	clientIdentifiersMu.RUnlock()
	if clientIdentifier == nil {
		clientIdentifier = tp.GetClientIp
	}

	return func(next func(ctx *requestContext) error) func(ctx *requestContext) error {
		return func(ctx *requestContext) error {
			if !serverLimiter.Allow() {
				return errRateLimited
			}
			if clientLimiters != nil {
				clientIdentity := clientIdentifier(ctx.Request)
				var clientLimiter *rate.Limiter
				if cached, exists := clientLimiters.Get(clientIdentity); exists {
					clientLimiter = cached.(*rate.Limiter)
				}
				if clientLimiter == nil {
					clientLimiter = rate.NewLimiter(rls.ClientTokenRate, rls.ClientTokenBucketSize)
					clientLimiters.Set(clientIdentity, clientLimiter, defaultExpiration)
				}
				if !clientLimiter.Allow() {
					return errRateLimited
				}
			}
			return next(ctx)
		}
	}
}

// fallbackTokenBucketSize returns the token bucket size to use for the given
// token rate. A token bucket of zero size never grants a token, so it would
// reject every request. A bucket size that is not specified therefore falls
// back to the token rate, rounded up to at least one token.
func fallbackTokenBucketSize(limit rate.Limit, bucketSize int) int {
	if bucketSize > 0 {
		return bucketSize
	}
	if limit <= 0 || limit == rate.Inf {
		return bucketSize
	}
	size := int(math.Ceil(float64(limit)))
	if size < 1 {
		size = 1
	}
	return size
}

// Rate limiting is applied both per endpoint and per client.
// Each endpoint has its own token bucket, and each client has its own token bucket as well.
type rateLimiterSettings struct {
	// limitation to server
	TokenRate       rate.Limit `map:"token_rate"` // rate of token generation, eg. 2 - 2 events every sec; 0.5 - 1 event every 2 sec
	TokenBucketSize int        `map:"token_bucket_size"`

	// limitation to client
	ClientIdentifierType  string     `map:"client_identifier_type"` // eg. "IP"
	ClientTokenRate       rate.Limit `map:"client_token_rate"`
	ClientTokenBucketSize int        `map:"client_token_bucket_size"`
}

func getRateLimiterSettings(limiterName string) *rateLimiterSettings {
	srls, _ := def.GetObj[map[string]*rateLimiterSettings]("app.limiters")
	if len(srls) == 0 {
		return nil
	}
	if cbs, ok := srls[limiterName]; ok {
		return cbs
	}
	return nil
}

func newCorsInterceptor() interceptorFunc {
	corsSettings := getCorsSettings()
	if corsSettings == nil {
		return nil
	}
	// pre-parse allowed origin URLs
	parsedOrigins := make([]*url.URL, len(corsSettings.AllowOrigins))
	for i, allowOrigin := range corsSettings.AllowOrigins {
		u, err := url.Parse(allowOrigin)
		if err == nil {
			parsedOrigins[i] = u
		}
	}
	return func(next func(*requestContext) error) func(ctx *requestContext) error {
		return func(ctx *requestContext) error {
			origin := ctx.Request.Header.Get("Origin")
			if origin != "" && len(corsSettings.AllowOrigins) > 0 {
				for i, allowOrigin := range corsSettings.AllowOrigins {
					if origin == allowOrigin || strings.HasSuffix(origin, "." + allowOrigin) {
						ctx.Writer.Header().Set("Access-Control-Allow-Origin", origin)
					} else {
						originUrl, err := url.Parse(origin)
						if err != nil {
							return errs.Wrap(err, "failed to parse origin")
						}
						if parsedOrigins[i] != nil && originUrl.Scheme == parsedOrigins[i].Scheme && strings.HasSuffix(originUrl.Host, strings.Replace(parsedOrigins[i].Host, "*", "", -1)) {
							ctx.Writer.Header().Set("Access-Control-Allow-Origin", origin)
						}
					}
				}
			}
			if len(corsSettings.AllowMethods) > 0 {
				ctx.Writer.Header().Set("Access-Control-Allow-Methods", strings.Join(corsSettings.AllowMethods, ","))
			}
			if len(corsSettings.AllowHeaders) > 0 {
				ctx.Writer.Header().Set("Access-Control-Allow-Headers", strings.Join(corsSettings.AllowHeaders, ","))
			}
			if corsSettings.AllowCredentials != nil && *corsSettings.AllowCredentials {
				ctx.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if corsSettings.MaxAge > 0 {
				ctx.Writer.Header().Set("Access-Control-Max-Age", strconv.Itoa(corsSettings.MaxAge))
			}
			return next(ctx)
		}
	}
}

func getCorsSettings() *corsSettings {
	cs, _ := def.GetObj[*corsSettings]("app.cors")
	if cs == nil {
		return &corsSettings{}
	}
	return cs
}

type corsSettings struct {
	AllowOrigins     []string `map:"allow_origins"`
	AllowMethods     []string `map:"allow_methods"`
	AllowHeaders     []string `map:"allow_headers"`
	AllowCredentials *bool    `map:"allow_credentials"`
	MaxAge           int      `map:"max_age"`
}

func recoverInterceptor(next func(ctx *requestContext) error) func(ctx *requestContext) error {
	return func(ctx *requestContext) error {
		defer func() {
			if r := recover(); r != nil {
				log.Error("Panic recovered in endpoint: {0} {1}", ctx.Request.Method, ctx.Request.URL.Path)
				defaultErrHandler(ctx, errs.New("Internal Server Error").WithStatus(http.StatusInternalServerError))
			}
		}()
		return next(ctx)
	}
}
