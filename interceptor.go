package sprout

import (
	"errors"
	"github.com/patrickmn/go-cache"
	"github.com/wxy365/basal/env"
	"github.com/wxy365/basal/lei"
	"github.com/wxy365/basal/tp"
	"golang.org/x/time/rate"
	"net/http"
	"time"
)
import "github.com/sony/gobreaker"

type Interceptor func(next func(w http.ResponseWriter, r *http.Request) error) func(w http.ResponseWriter, r *http.Request) error

type circuitBreakerSettings struct {
	MaxRequests            uint32        `json:"max_requests"`
	Interval               time.Duration `json:"interval"`
	Timeout                time.Duration `json:"timeout"`
	MaxConsecutiveFailures uint32        `json:"max_consecutive_failures"`
	MaxFailureRatio        float64       `json:"failure_ratio"`
}

func getCircuitBreakerSettings(breakerName string) *circuitBreakerSettings {
	scbs, _ := env.GetObj[map[string]*circuitBreakerSettings]("SPROUT_CIRCUIT_BREAKER_SETTINGS")
	if len(scbs) == 0 {
		return nil
	}
	if cbs, ok := scbs[breakerName]; ok {
		return cbs
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
			lei.Info("Circuit breaker state changed, name: '{0}', from: '{1}', to: '{2}'", name, from, to)
		},
	})
}

// newCircuitBreakerInterceptor creates a circuit breaker with the given name.
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
	return func(next func(w http.ResponseWriter, r *http.Request) error) func(w http.ResponseWriter, r *http.Request) error {
		return func(w http.ResponseWriter, r *http.Request) error {
			done, err := breaker.Allow()
			if err != nil {
				return ErrCircuitBroken
			}
			defer func() {
				isSuccessful := true
				if er := r.Context().Value(ctxKeyEndpointError); er != nil {
					err = er.(error)
					var e *lei.Err
					if errors.As(err, &e) {
						if e.Status >= http.StatusInternalServerError {
							isSuccessful = false
						}
					}
				}
				done(isSuccessful)
			}()
			return next(w, r)
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

// The rate limiter controls the traffic at both the source (client) and the server interface simultaneously
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
		clientIdentifier = tp.GetUserIp
	}

	return func(next func(w http.ResponseWriter, r *http.Request) error) func(w http.ResponseWriter, r *http.Request) error {
		return func(w http.ResponseWriter, r *http.Request) error {
			if !serverLimiter.Allow() {
				return ErrRateLimited
			}
			if clientLimiters != nil {
				clientIdentity := clientIdentifier(r)
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
			return next(w, r)
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
	srls, _ := env.GetObj[map[string]*rateLimiterSettings]("SPROUT_RATE_LIMITER_SETTINGS")
	if len(srls) == 0 {
		return nil
	}
	if cbs, ok := srls[limiterName]; ok {
		return cbs
	}
	return nil
}
