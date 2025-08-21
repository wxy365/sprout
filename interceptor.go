package sprout

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/sony/gobreaker"
	"github.com/wxy365/basal/cfg/def"
	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
	"github.com/wxy365/basal/tp"
	"golang.org/x/time/rate"
)

type Interceptor func(next func(*Context) error) func(ctx *Context) error

type circuitBreakerSettings struct {
	MaxRequests            uint32        `json:"max_requests"`
	Interval               time.Duration `json:"interval"`
	Timeout                time.Duration `json:"timeout"`
	MaxConsecutiveFailures uint32        `json:"max_consecutive_failures"`
	MaxFailureRatio        float64       `json:"failure_ratio"`
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
func newCircuitBreakerInterceptor(breakerName string) Interceptor {
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
	return func(next func(ctx *Context) error) func(ctx *Context) error {
		return func(ctx *Context) error {
			done, err := breaker.Allow()
			if err != nil {
				return ErrCircuitBroken
			}
			defer func() {
				isSuccessful := true
				if er := ctx.Value(ctxKeyEndpointError); er != nil {
					err = er.(error)
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
	// This map contains the names and function definitions of client identifier, used in client rate limiting.
	// Callers can register client identifiers to this map through RegisterClientIdentifier.
	clientIdentifiers = make(map[string]ClientIdentifier)
)

type ClientIdentifier func(*http.Request) string

func RegisterClientIdentifier(name string, identifier ClientIdentifier) {
	clientIdentifiers[name] = identifier
}

// The rate limiter controls the traffic at both the client and the server interface simultaneously
func newRateLimiterInterceptor(limiterName string) Interceptor {
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
		if rls.ClientTokenRate < 1 {
			defaultExpiration = time.Second * time.Duration(1/rls.ClientTokenRate)
		}
		clientLimiters = cache.New(defaultExpiration, time.Second)
	}
	serverLimiter := rate.NewLimiter(rls.TokenRate, rls.TokenBucketSize)

	clientIdentifier := clientIdentifiers[rls.ClientIdentifierType]
	if clientIdentifier == nil {
		clientIdentifier = tp.GetClientIp
	}

	return func(next func(ctx *Context) error) func(ctx *Context) error {
		return func(ctx *Context) error {
			if !serverLimiter.Allow() {
				return ErrRateLimited
			}
			if clientLimiters != nil {
				clientIdentity := clientIdentifier(ctx.Request)
				var clientLimiter *rate.Limiter
				if cached, exists := clientLimiters.Get(clientIdentity); !exists {
					clientLimiter = rate.NewLimiter(rls.ClientTokenRate, rls.ClientTokenBucketSize)
				} else {
					clientLimiter = cached.(*rate.Limiter)
				}
				clientLimiters.Set(clientIdentity, clientLimiter, defaultExpiration)
				if !clientLimiter.Allow() {
					return ErrRateLimited
				}
			}
			return next(ctx)
		}
	}
}

// Rate limiting is applied both per endpoint and per client.
// Each endpoint has its own token bucket, and each client has its own token bucket as well.
type rateLimiterSettings struct {
	// limitation to server
	TokenRate       rate.Limit `json:"token_rate"` // rate of token generation, eg. 2 - 2 events every sec; 0.5 - 1 event every 2 sec
	TokenBucketSize int        `json:"token_bucket_size"`

	// limitation to client
	ClientIdentifierType  string // eg. "IP"
	ClientTokenRate       rate.Limit
	ClientTokenBucketSize int
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

func newCorsInterceptor() Interceptor {
	corsSettings := getCorsSettings()
	if corsSettings == nil {
		return nil
	}
	return func(next func(*Context) error) func(ctx *Context) error {
		return func(ctx *Context) error {
			origin := ctx.Request.Header.Get("Origin")
			if origin != "" && len(corsSettings.AllowOrigins) > 0 {
				for _, allowOrigin := range corsSettings.AllowOrigins {
					if origin == allowOrigin || strings.HasSuffix(origin, allowOrigin) {
						ctx.Writer.Header().Set("Access-Control-Allow-Origin", origin)
					} else {
						originUrl, err := url.Parse(origin)
						if err != nil {
							return errs.Wrap(err, "failed to parse origin")
						}
						allowOriginUrl, _ := url.Parse(allowOrigin)
						if originUrl.Scheme == allowOriginUrl.Scheme && strings.HasSuffix(originUrl.Host, strings.Replace(allowOriginUrl.Host, "*", "", -1)) {
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
	AllowOrigins     []string `json:"allow_origins"`
	AllowMethods     []string `json:"allow_methods"`
	AllowHeaders     []string `json:"allow_headers"`
	AllowCredentials *bool    `json:"allow_credentials"`
	MaxAge           int      `json:"max_age"`
}
