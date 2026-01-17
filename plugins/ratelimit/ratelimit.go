package ratelimit

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
	"golang.org/x/time/rate"
)

// RateLimitPlugin implements token bucket rate limiting
type RateLimitPlugin struct {
	// Base plugin properties
	name     string
	priority int
	timeout  time.Duration
	failure  engine.FailureMode

	// Rate limiting configuration
	enabled           bool
	requestsPerSecond float64
	burst             int

	// Limiting strategy
	strategy LimitStrategy // per_ip, per_user, per_route, global

	// Storage
	limiters   map[string]*rateLimiter
	limitersMu sync.RWMutex

	// Cleanup
	cleanupInterval time.Duration
	cleanupDone     chan struct{}

	// Headers
	includeHeaders bool
	headerPrefix   string

	logger *logging.Logger
}

// LimitStrategy defines how rate limits are applied
type LimitStrategy string

const (
	StrategyPerIP    LimitStrategy = "per_ip"
	StrategyPerUser  LimitStrategy = "per_user"
	StrategyPerRoute LimitStrategy = "per_route"
	StrategyGlobal   LimitStrategy = "global"
)

// rateLimiter holds the token bucket limiter and metadata
type rateLimiter struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// NewRateLimitPlugin creates a new rate limiting plugin
func NewRateLimitPlugin(logger *logging.Logger) *RateLimitPlugin {
	plugin := &RateLimitPlugin{
		name:              "rate_limit",
		priority:          60,
		timeout:           1 * time.Second,
		failure:           engine.FailureModeDeny,
		requestsPerSecond: 100,
		burst:             200,
		strategy:          StrategyPerIP,
		limiters:          make(map[string]*rateLimiter),
		cleanupInterval:   5 * time.Minute,
		cleanupDone:       make(chan struct{}),
		includeHeaders:    true,
		headerPrefix:      "X-RateLimit",
		logger:            logger,
	}

	// Start cleanup goroutine
	go plugin.cleanupExpiredLimiters()

	return plugin
}

// Plugin interface implementation

func (p *RateLimitPlugin) Name() string {
	return p.name
}

func (p *RateLimitPlugin) Phase() engine.Phase {
	return engine.PhaseTraffic
}

func (p *RateLimitPlugin) Priority() int {
	return p.priority
}

func (p *RateLimitPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *RateLimitPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *RateLimitPlugin) Initialize(config map[string]interface{}) error {
	// Enabled flag
	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	}

	if !p.enabled {
		return nil
	}

	// Requests per second
	if rps, ok := config["requests_per_second"].(float64); ok {
		p.requestsPerSecond = rps
	} else if rps, ok := config["requests_per_second"].(int); ok {
		p.requestsPerSecond = float64(rps)
	}

	// Burst
	if burst, ok := config["burst"].(float64); ok {
		p.burst = int(burst)
	} else if burst, ok := config["burst"].(int); ok {
		p.burst = burst
	}

	// Strategy
	if strategy, ok := config["strategy"].(string); ok {
		p.strategy = LimitStrategy(strategy)
	}

	// Headers
	if includeHeaders, ok := config["include_headers"].(bool); ok {
		p.includeHeaders = includeHeaders
	}

	if headerPrefix, ok := config["header_prefix"].(string); ok {
		p.headerPrefix = headerPrefix
	}

	// Validate configuration
	if p.requestsPerSecond <= 0 {
		return fmt.Errorf("requests_per_second must be positive")
	}

	if p.burst <= 0 {
		return fmt.Errorf("burst must be positive")
	}

	p.logger.Info("Rate limit plugin initialized",
		logging.Float64("rps", p.requestsPerSecond),
		logging.Int("burst", p.burst),
		logging.String("strategy", string(p.strategy)),
	)

	return nil
}

func (p *RateLimitPlugin) Execute(ctx *engine.ExecutionContext) error {
	if !p.enabled {
		return nil
	}

	// Determine limiting key based on strategy
	key, err := p.getLimitKey(ctx)
	if err != nil {
		return fmt.Errorf("failed to get limit key: %w", err)
	}

	// Get or create limiter for this key
	limiter := p.getLimiter(key)

	// Try to consume a token
	reservation := limiter.limiter.Reserve()
	if !reservation.OK() {
		// Rate limit exceeded
		p.logger.Warn("Rate limit exceeded",
			logging.String("key", key),
			logging.String("strategy", string(p.strategy)),
			logging.String("request_id", ctx.RequestID),
		)

		// Add rate limit info to context
		ctx.SetState("rate_limited", true)
		ctx.SetState("rate_limit_key", key)

		// Set response headers if enabled
		if p.includeHeaders {
			p.setRateLimitHeaders(ctx, limiter.limiter, true)
		}

		return fmt.Errorf("rate limit exceeded for %s", key)
	}

	// Check if we need to wait
	delay := reservation.Delay()
	if delay > 0 {
		// For now, we don't wait - just reject
		// In production, you might want to implement queuing
		reservation.Cancel()

		ctx.SetState("rate_limited", true)
		ctx.SetState("rate_limit_retry_after", delay.Seconds())

		if p.includeHeaders {
			p.setRateLimitHeaders(ctx, limiter.limiter, true)
		}

		return fmt.Errorf("rate limit exceeded, retry after %s", delay)
	}

	// Request allowed
	ctx.SetState("rate_limited", false)

	// Set response headers
	if p.includeHeaders {
		p.setRateLimitHeaders(ctx, limiter.limiter, false)
	}

	p.logger.Debug("Rate limit check passed",
		logging.String("key", key),
		logging.String("request_id", ctx.RequestID),
	)

	return nil
}

// getLimitKey determines the key for rate limiting based on strategy
func (p *RateLimitPlugin) getLimitKey(ctx *engine.ExecutionContext) (string, error) {
	switch p.strategy {
	case StrategyPerIP:
		return ctx.ClientIP, nil

	case StrategyPerUser:
		// Try to get user from JWT claims
		if claims, ok := ctx.GetState("jwt_claims"); ok {
			if jwtClaims, ok := claims.(map[string]interface{}); ok {
				if userID, ok := jwtClaims["user_id"].(string); ok && userID != "" {
					return userID, nil
				}
				if sub, ok := jwtClaims["sub"].(string); ok && sub != "" {
					return sub, nil
				}
			}
		}
		// Fallback to IP if no user found
		return ctx.ClientIP, nil

	case StrategyPerRoute:
		return ctx.RouteID, nil

	case StrategyGlobal:
		return "global", nil

	default:
		return "", fmt.Errorf("unknown strategy: %s", p.strategy)
	}
}

// getLimiter gets or creates a rate limiter for a key
func (p *RateLimitPlugin) getLimiter(key string) *rateLimiter {
	p.limitersMu.RLock()
	limiter, exists := p.limiters[key]
	p.limitersMu.RUnlock()

	if exists {
		limiter.lastAccess = time.Now()
		return limiter
	}

	// Create new limiter
	p.limitersMu.Lock()
	defer p.limitersMu.Unlock()

	// Double-check after acquiring write lock
	if limiter, exists := p.limiters[key]; exists {
		limiter.lastAccess = time.Now()
		return limiter
	}

	limiter = &rateLimiter{
		limiter:    rate.NewLimiter(rate.Limit(p.requestsPerSecond), p.burst),
		lastAccess: time.Now(),
	}

	p.limiters[key] = limiter

	p.logger.Debug("Created new rate limiter",
		logging.String("key", key),
		logging.Float64("rps", p.requestsPerSecond),
		logging.Int("burst", p.burst),
	)

	return limiter
}

// setRateLimitHeaders sets rate limit information in response headers
func (p *RateLimitPlugin) setRateLimitHeaders(ctx *engine.ExecutionContext, limiter *rate.Limiter, limited bool) {
	// Get response writer from context
	resp, ok := ctx.GetState("http_response")
	if !ok {
		return
	}

	w, ok := resp.(http.ResponseWriter)
	if !ok {
		return
	}

	// Set standard rate limit headers
	w.Header().Set(p.headerPrefix+"-Limit", fmt.Sprintf("%d", p.burst))

	// Calculate remaining tokens
	// Note: rate.Limiter doesn't expose token count directly
	// This is an approximation
	remaining := p.burst
	if limited {
		remaining = 0
	}
	w.Header().Set(p.headerPrefix+"-Remaining", fmt.Sprintf("%d", remaining))

	// Set reset time (next second)
	resetTime := time.Now().Add(1 * time.Second).Unix()
	w.Header().Set(p.headerPrefix+"-Reset", fmt.Sprintf("%d", resetTime))

	// Set retry-after header if limited
	if limited {
		w.Header().Set("Retry-After", "1")
	}
}

// cleanupExpiredLimiters removes limiters that haven't been used recently
func (p *RateLimitPlugin) cleanupExpiredLimiters() {
	ticker := time.NewTicker(p.cleanupInterval)
	defer ticker.Stop()

	expirationThreshold := 10 * time.Minute

	for {
		select {
		case <-ticker.C:
			p.limitersMu.Lock()

			now := time.Now()
			expiredKeys := make([]string, 0)

			for key, limiter := range p.limiters {
				if now.Sub(limiter.lastAccess) > expirationThreshold {
					expiredKeys = append(expiredKeys, key)
				}
			}

			for _, key := range expiredKeys {
				delete(p.limiters, key)
			}

			p.limitersMu.Unlock()

			if len(expiredKeys) > 0 {
				p.logger.Debug("Cleaned up expired rate limiters",
					logging.Int("count", len(expiredKeys)),
				)
			}

		case <-p.cleanupDone:
			return
		}
	}
}

// Close stops the cleanup goroutine
func (p *RateLimitPlugin) Close() error {
	close(p.cleanupDone)
	return nil
}

// Health check
func (p *RateLimitPlugin) Health() error {
	if !p.enabled {
		return fmt.Errorf("rate limit plugin is disabled")
	}

	if p.requestsPerSecond <= 0 {
		return fmt.Errorf("invalid rate: %f", p.requestsPerSecond)
	}

	return nil
}

// GetStats returns statistics about rate limiting
func (p *RateLimitPlugin) GetStats() map[string]interface{} {
	p.limitersMu.RLock()
	defer p.limitersMu.RUnlock()

	return map[string]interface{}{
		"active_limiters":     len(p.limiters),
		"requests_per_second": p.requestsPerSecond,
		"burst":               p.burst,
		"strategy":            string(p.strategy),
	}
}

// ResetLimiter removes the rate limiter for a specific key
func (p *RateLimitPlugin) ResetLimiter(key string) {
	p.limitersMu.Lock()
	defer p.limitersMu.Unlock()

	delete(p.limiters, key)

	p.logger.Info("Rate limiter reset",
		logging.String("key", key),
	)
}

// ResetAllLimiters clears all rate limiters
func (p *RateLimitPlugin) ResetAllLimiters() {
	p.limitersMu.Lock()
	defer p.limitersMu.Unlock()

	count := len(p.limiters)
	p.limiters = make(map[string]*rateLimiter)

	p.logger.Info("All rate limiters reset",
		logging.Int("count", count),
	)
}
