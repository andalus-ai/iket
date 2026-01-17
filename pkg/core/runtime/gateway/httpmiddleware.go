package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/bhangun/iket/pkg/core/compiler"
	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
	"github.com/google/uuid"
)

// HTTPMiddleware handles HTTP request/response integration with the engine
type HTTPMiddleware struct {
	engine   *engine.Engine
	compiler *compiler.Compiler
	logger   *logging.Logger
}

// NewHTTPMiddleware creates a new HTTP middleware
func NewHTTPMiddleware(eng *engine.Engine, comp *compiler.Compiler, logger *logging.Logger) *HTTPMiddleware {
	return &HTTPMiddleware{
		engine:   eng,
		compiler: comp,
		logger:   logger,
	}
}

// HandleRequest processes an HTTP request through the engine pipeline
func (m *HTTPMiddleware) HandleRequest(w http.ResponseWriter, r *http.Request, route *compiler.CompiledRoute) error {
	// Extract or generate request ID
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.New().String()
	}

	// Get client IP
	clientIP := extractClientIP(r)

	// Create execution context
	execCtx := engine.NewExecutionContext(
		r.Context(),
		requestID,
		clientIP,
		route.ID,
	)

	// Store HTTP request and response in context
	execCtx.SetState("http_request", r)
	execCtx.SetState("http_response", w)
	execCtx.SetState("compiled_route", route)

	// Store request metadata
	execCtx.SetState("request_method", r.Method)
	execCtx.SetState("request_path", r.URL.Path)
	execCtx.SetState("request_host", r.Host)

	// Set response headers
	w.Header().Set("X-Request-ID", requestID)

	// Execute pipeline
	if err := m.engine.Execute(execCtx, route.Pipeline); err != nil {
		m.logger.Error("Pipeline execution failed",
			err,
			logging.String("request_id", requestID),
			logging.String("route", route.ID),
		)

		return m.handleError(w, err, execCtx)
	}

	// Check if response was already written by upstream plugin
	if _, ok := execCtx.GetState("response_written"); !ok {
		// If upstream didn't write response, return success
		w.WriteHeader(http.StatusOK)
	}

	// Log execution stats
	stats := execCtx.GetStats()
	m.logger.Info("Request completed",
		logging.String("request_id", requestID),
		logging.Duration("total_duration", stats.TotalDuration),
		logging.Int("phases", stats.PhaseCount),
		logging.Int("plugins", stats.PluginCount),
		logging.String("slowest_phase", string(stats.SlowestPhase)),
	)

	return nil
}

// handleError handles pipeline execution errors
func (m *HTTPMiddleware) handleError(w http.ResponseWriter, err error, ctx *engine.ExecutionContext) error {
	// Check if it's a rate limit error
	if rateLimited, ok := ctx.GetState("rate_limited"); ok && rateLimited.(bool) {
		retryAfter, _ := ctx.GetState("rate_limit_retry_after")

		w.Header().Set("Content-Type", "application/json")
		if retryAfter != nil {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.(float64)))
		}
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limit_exceeded","message":"Rate limit exceeded"}`))
		return nil
	}

	// Check if it's a circuit breaker error
	if circuitOpen, ok := ctx.GetState("circuit_open"); ok && circuitOpen.(bool) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"circuit_open","message":"Service temporarily unavailable"}`))
		return nil
	}

	// Check if it's an auth error
	if _, ok := ctx.GetState("jwt_validated"); !ok {
		if strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "auth") {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized","message":"Authentication required"}`))
			return nil
		}
	}

	// Check if it's a policy error
	if strings.Contains(err.Error(), "policy") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden","message":"Access denied by policy"}`))
		return nil
	}

	// Default error response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	w.Write([]byte(fmt.Sprintf(`{"error":"gateway_error","message":"%s"}`, sanitizeErrorMessage(err.Error()))))

	return nil
}

// extractClientIP extracts the client IP from the request
func extractClientIP(r *http.Request) string {
	// Try X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Try X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Try Forwarded header (RFC 7239)
	if fwd := r.Header.Get("Forwarded"); fwd != "" {
		// Parse "for=192.0.2.60;proto=http;by=203.0.113.43"
		parts := strings.Split(fwd, ";")
		for _, part := range parts {
			if strings.HasPrefix(part, "for=") {
				ip := strings.TrimPrefix(part, "for=")
				ip = strings.Trim(ip, "\"")
				return ip
			}
		}
	}

	// Fallback to RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}

// sanitizeErrorMessage removes sensitive information from error messages
func sanitizeErrorMessage(msg string) string {
	// Remove potential sensitive data
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")

	// Limit length
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}

	return msg
}

// RequestIDMiddleware adds request ID to every request
func RequestIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = uuid.New().String()
			}

			// Add to response header
			w.Header().Set("X-Request-ID", requestID)

			// Add to context
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CORSMiddleware handles CORS preflight and headers
func CORSMiddleware(allowedOrigins []string, allowedMethods []string, allowedHeaders []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Check if origin is allowed
			allowed := false
			for _, allowedOrigin := range allowedOrigins {
				if allowedOrigin == "*" || allowedOrigin == origin {
					allowed = true
					break
				}
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowedMethods, ", "))
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			// Handle preflight
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RecoveryMiddleware recovers from panics and returns 500
func RecoveryMiddleware(logger *logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					requestID, _ := r.Context().Value(requestIDKey).(string)
					if requestID == "" {
						requestID = "unknown"
					}

					logger.Error("Request panic recovered",
						fmt.Errorf("%v", err),
						logging.String("request_id", requestID),
						logging.String("path", r.URL.Path),
						logging.String("method", r.Method),
					)

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte(`{"error":"internal_error","message":"An unexpected error occurred"}`))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// TimeoutMiddleware adds a timeout to the request
func TimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			// Create done channel
			done := make(chan struct{})

			go func() {
				next.ServeHTTP(w, r.WithContext(ctx))
				close(done)
			}()

			select {
			case <-done:
				// Request completed normally
			case <-ctx.Done():
				// Request timed out
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusGatewayTimeout)
				w.Write([]byte(`{"error":"timeout","message":"Request timeout"}`))
			}
		})
	}
}

// CompressionMiddleware adds gzip compression support
func CompressionMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if client accepts gzip
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}

			// Check content type (only compress text types)
			// This is a simplified check - in production use a more comprehensive list

			// Create gzip writer
			// Note: Implement actual gzip compression wrapper here
			// For now, just pass through
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeadersMiddleware adds security headers
func SecurityHeadersMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Security headers
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-XSS-Protection", "1; mode=block")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Content-Security-Policy", "default-src 'self'")

			// Remove server header
			w.Header().Del("Server")

			next.ServeHTTP(w, r)
		})
	}
}
