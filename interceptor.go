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

// circuitBreakerGuard 封装熔断器的放行与结果上报。
// 它不依赖具体的传输方式，因此可以被 HTTP 与 gRPC 共用。
//
// The exported alias CircuitBreakerGuard is declared in api.go, which collects
// the public API surface of the package.
type circuitBreakerGuard struct {
	breaker *gobreaker.TwoStepCircuitBreaker
}

// newCircuitBreakerGuard 按名称创建熔断守卫，配置取自 app.breakers。
// 未配置时使用默认配置。
func newCircuitBreakerGuard(breakerName string) *circuitBreakerGuard {
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
	return &circuitBreakerGuard{breaker: newCircuitBreaker(cbs)}
}

// Allow 放行一次调用，返回的结果上报函数用于标记本次调用是否成功。
func (g *circuitBreakerGuard) Allow() (func(bool), error) {
	return g.breaker.Allow()
}

// newCircuitBreakerInterceptor creates a circuit breaker with the given Name.
// If the settings for the breaker is not configured, then the default settings is applied.
func newCircuitBreakerInterceptor(breakerName string) interceptorFunc {
	guard := newCircuitBreakerGuard(breakerName)
	return func(next func(ctx *requestContext) error) func(ctx *requestContext) error {
		return func(ctx *requestContext) error {
			done, err := guard.Allow()
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

// rateLimiterGuard 封装令牌桶的放行判断。
// 它不依赖具体的传输方式：客户端维度的标识由调用方给出，因此 HTTP 与 gRPC
// 都可以复用同一份限流配置与判定逻辑。
//
// The exported alias RateLimiterGuard is declared in api.go, which collects
// the public API surface of the package.
type rateLimiterGuard struct {
	// serverLimiter 是服务端维度的令牌桶。
	serverLimiter *rate.Limiter
	// clientLimiters 是客户端维度的令牌桶缓存，未启用客户端维度限流时为 nil。
	clientLimiters *cache.Cache
	// clientTokenRate 是客户端维度的令牌生成速率。
	clientTokenRate rate.Limit
	// clientTokenBucketSize 是客户端维度的令牌桶容量。
	clientTokenBucketSize int
	// defaultExpiration 是客户端令牌桶的过期时长。
	defaultExpiration time.Duration
}

// newRateLimiterGuard 按名称创建限流守卫，配置取自 app.limiters。
// 未配置时使用默认配置。
func newRateLimiterGuard(limiterName string) *rateLimiterGuard {
	rls := getRateLimiterSettings(limiterName)
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
	}
	g := &rateLimiterGuard{
		serverLimiter:          rate.NewLimiter(rls.TokenRate, rls.TokenBucketSize),
		clientTokenRate:        rls.ClientTokenRate,
		clientTokenBucketSize:  rls.ClientTokenBucketSize,
		defaultExpiration:      time.Second,
	}
	if rls.ClientTokenRate > 0 {
		if rls.ClientTokenRate < 1 {
			g.defaultExpiration = time.Second * time.Duration(1/rls.ClientTokenRate)
		}
		g.clientLimiters = cache.New(g.defaultExpiration, time.Second)
	}
	return g
}

// Allow 放行一次调用。clientIdentity 为空表示不做客户端维度的限流。
func (g *rateLimiterGuard) Allow(clientIdentity string) bool {
	if !g.serverLimiter.Allow() {
		return false
	}
	if g.clientLimiters == nil || clientIdentity == "" {
		return true
	}
	var clientLimiter *rate.Limiter
	if cached, exists := g.clientLimiters.Get(clientIdentity); exists {
		clientLimiter = cached.(*rate.Limiter)
	}
	if clientLimiter == nil {
		clientLimiter = rate.NewLimiter(g.clientTokenRate, g.clientTokenBucketSize)
		g.clientLimiters.Set(clientIdentity, clientLimiter, g.defaultExpiration)
	}
	return clientLimiter.Allow()
}

// The rate limiter controls the traffic at both the client and the server interface simultaneously
//
// A limiter that is not configured is not applied to the client side at all. A
// limiter that is configured applies the client side limit only when a client
// token rate is set, because a limiter without a token rate rejects every
// request.
func newRateLimiterInterceptor(limiterName string) interceptorFunc {
	guard := newRateLimiterGuard(limiterName)

	var identifierType string
	if rls := getRateLimiterSettings(limiterName); rls != nil {
		identifierType = rls.ClientIdentifierType
	}
	clientIdentifiersMu.RLock()
	clientIdentifier := clientIdentifiers[identifierType]
	clientIdentifiersMu.RUnlock()
	if clientIdentifier == nil {
		clientIdentifier = tp.GetClientIp
	}

	return func(next func(ctx *requestContext) error) func(ctx *requestContext) error {
		return func(ctx *requestContext) error {
			var clientIdentity string
			if guard.clientLimiters != nil {
				clientIdentity = clientIdentifier(ctx.Request)
			}
			if !guard.Allow(clientIdentity) {
				return errRateLimited
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

// corsOrigin is the pre-computed form of one allowed origin.
type corsOrigin struct {
	// origin is the allowed origin, as it is configured.
	origin string
	// suffix is the allowed origin with a leading dot, which matches the
	// subdomains of the origin.
	suffix string
	// scheme is the scheme of the allowed origin.
	scheme string
	// host is the host of the allowed origin, without its wildcard.
	host string
	// valid tells whether the allowed origin could be parsed.
	valid bool
}

func newCorsInterceptor() interceptorFunc {
	corsSettings := getCorsSettings()
	if corsSettings == nil {
		return nil
	}
	// Pre-compute the values that do not change from one request to the next,
	// so that the interceptor does not rebuild them for every request.
	origins := make([]corsOrigin, len(corsSettings.AllowOrigins))
	for i, allowOrigin := range corsSettings.AllowOrigins {
		origin := corsOrigin{origin: allowOrigin, suffix: "." + allowOrigin}
		if u, err := url.Parse(allowOrigin); err == nil {
			origin.scheme = u.Scheme
			origin.host = strings.ReplaceAll(u.Host, "*", "")
			origin.valid = true
		}
		origins[i] = origin
	}
	allowMethods := strings.Join(corsSettings.AllowMethods, ",")
	allowHeaders := strings.Join(corsSettings.AllowHeaders, ",")
	maxAge := ""
	if corsSettings.MaxAge > 0 {
		maxAge = strconv.Itoa(corsSettings.MaxAge)
	}
	return func(next func(*requestContext) error) func(ctx *requestContext) error {
		return func(ctx *requestContext) error {
			origin := ctx.Request.Header.Get("Origin")
			if origin != "" && len(origins) > 0 {
				// The origin of the request is parsed at most once.
				var originUrl *url.URL
				var originErr error
				parsed := false
				for _, allowed := range origins {
					if origin == allowed.origin || strings.HasSuffix(origin, allowed.suffix) {
						ctx.Writer.Header().Set("Access-Control-Allow-Origin", origin)
						continue
					}
					if !parsed {
						originUrl, originErr = url.Parse(origin)
						parsed = true
					}
					if originErr != nil {
						return errs.Wrap(originErr, "failed to parse origin")
					}
					if allowed.valid && originUrl.Scheme == allowed.scheme && strings.HasSuffix(originUrl.Host, allowed.host) {
						ctx.Writer.Header().Set("Access-Control-Allow-Origin", origin)
					}
				}
			}
			if allowMethods != "" {
				ctx.Writer.Header().Set("Access-Control-Allow-Methods", allowMethods)
			}
			if allowHeaders != "" {
				ctx.Writer.Header().Set("Access-Control-Allow-Headers", allowHeaders)
			}
			if corsSettings.AllowCredentials != nil && *corsSettings.AllowCredentials {
				ctx.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if maxAge != "" {
				ctx.Writer.Header().Set("Access-Control-Max-Age", maxAge)
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
