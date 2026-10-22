package upstream

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
)

// UpstreamProxyPlugin handles proxying requests to upstream services
type UpstreamProxyPlugin struct {
	// Base plugin properties
	name     string
	priority int
	timeout  time.Duration
	failure  engine.FailureMode

	// Proxy configuration
	enabled             bool
	preserveHost        bool
	stripPath           bool
	addForwardedHeaders bool

	// Connection pooling
	maxIdleConns        int
	maxIdleConnsPerHost int
	idleConnTimeout     time.Duration

	// Retries
	maxRetries        int
	retryableStatuses map[int]bool

	// Transport
	transport *http.Transport

	logger *logging.Logger
	mu     sync.RWMutex
}

// NewUpstreamProxyPlugin creates a new upstream proxy plugin
func NewUpstreamProxyPlugin(logger *logging.Logger) *UpstreamProxyPlugin {
	plugin := &UpstreamProxyPlugin{
		name:                "upstream_proxy",
		priority:            10, // Low priority, runs in upstream phase
		timeout:             30 * time.Second,
		failure:             engine.FailureModeDeny,
		preserveHost:        false,
		stripPath:           false,
		addForwardedHeaders: true,
		maxIdleConns:        100,
		maxIdleConnsPerHost: 10,
		idleConnTimeout:     90 * time.Second,
		maxRetries:          0,
		retryableStatuses: map[int]bool{
			502: true,
			503: true,
			504: true,
		},
		logger: logger,
	}

	plugin.initializeTransport()

	return plugin
}

// Plugin interface implementation

func (p *UpstreamProxyPlugin) Name() string {
	return p.name
}

func (p *UpstreamProxyPlugin) Phase() engine.Phase {
	return engine.PhaseUpstream
}

func (p *UpstreamProxyPlugin) Priority() int {
	return p.priority
}

func (p *UpstreamProxyPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *UpstreamProxyPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *UpstreamProxyPlugin) Initialize(config map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Enabled flag
	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	} else {
		p.enabled = true // Default to enabled
	}

	// Preserve host header
	if preserveHost, ok := config["preserve_host"].(bool); ok {
		p.preserveHost = preserveHost
	}

	// Strip path prefix
	if stripPath, ok := config["strip_path"].(bool); ok {
		p.stripPath = stripPath
	}

	// Add X-Forwarded-* headers
	if addHeaders, ok := config["add_forwarded_headers"].(bool); ok {
		p.addForwardedHeaders = addHeaders
	}

	// Connection pool settings
	if maxIdle, ok := config["max_idle_conns"].(float64); ok {
		p.maxIdleConns = int(maxIdle)
	}

	if maxIdlePerHost, ok := config["max_idle_conns_per_host"].(float64); ok {
		p.maxIdleConnsPerHost = int(maxIdlePerHost)
	}

	if idleTimeout, ok := config["idle_conn_timeout"].(float64); ok {
		p.idleConnTimeout = time.Duration(idleTimeout) * time.Second
	}

	// Retry configuration
	if maxRetries, ok := config["max_retries"].(float64); ok {
		p.maxRetries = int(maxRetries)
	}

	// Timeout
	if timeout, ok := config["timeout"].(float64); ok {
		p.timeout = time.Duration(timeout) * time.Second
	}

	// Reinitialize transport with new settings
	p.initializeTransport()

	p.logger.Info("Upstream proxy plugin initialized",
		logging.Bool("preserve_host", p.preserveHost),
		logging.Bool("strip_path", p.stripPath),
		logging.Int("max_idle_conns", p.maxIdleConns),
		logging.Duration("timeout", p.timeout),
	)

	return nil
}

func (p *UpstreamProxyPlugin) Execute(ctx *engine.ExecutionContext) error {
	if !p.enabled {
		return nil
	}

	// Get HTTP request and response from context
	req, ok := ctx.GetState("http_request")
	if !ok {
		return fmt.Errorf("http request not found in context")
	}
	httpReq := req.(*http.Request)

	resp, ok := ctx.GetState("http_response")
	if !ok {
		return fmt.Errorf("http response not found in context")
	}
	w := resp.(http.ResponseWriter)

	// Get compiled route from context
	route, ok := ctx.GetState("compiled_route")
	if !ok {
		return fmt.Errorf("compiled route not found in context")
	}

	// Extract upstream URL from route
	upstream := p.getUpstreamURL(route)
	if upstream == "" {
		return fmt.Errorf("no upstream configured for route")
	}

	// Parse upstream URL
	upstreamURL, err := url.Parse(upstream)
	if err != nil {
		return fmt.Errorf("invalid upstream URL: %w", err)
	}

	// Proxy the request
	startTime := time.Now()
	err = p.proxyRequest(ctx, w, httpReq, upstreamURL)
	duration := time.Since(startTime)

	// Store upstream response info in context
	ctx.SetState("upstream_duration", duration)
	ctx.SetState("upstream_url", upstream)

	if err != nil {
		p.logger.Error("Upstream proxy failed",
			err,
			logging.String("upstream", upstream),
			logging.String("request_id", ctx.RequestID),
			logging.Duration("duration", duration),
		)
		return err
	}

	p.logger.Debug("Upstream proxy successful",
		logging.String("upstream", upstream),
		logging.String("request_id", ctx.RequestID),
		logging.Duration("duration", duration),
	)

	return nil
}

// proxyRequest performs the actual proxying
func (p *UpstreamProxyPlugin) proxyRequest(ctx *engine.ExecutionContext, w http.ResponseWriter, r *http.Request, upstream *url.URL) error {
	// Create reverse proxy
	proxy := p.createReverseProxy(upstream)

	// Prepare request
	p.prepareRequest(r, upstream)

	// Set up error handling
	var lastErr error
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		lastErr = err
		p.logger.Error("Proxy error",
			err,
			logging.String("upstream", upstream.String()),
			logging.String("path", r.URL.Path),
		)
	}

	// Attempt proxy with retries
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if attempt > 0 {
			p.logger.Debug("Retrying request",
				logging.Int("attempt", attempt),
				logging.String("upstream", upstream.String()),
			)
		}

		// Create a response recorder to capture the response
		recorder := &responseRecorder{
			ResponseWriter: w,
			statusCode:     0,
		}

		// Proxy the request
		proxy.ServeHTTP(recorder, r)

		// Check if retry is needed
		if !p.shouldRetry(recorder.statusCode, attempt) {
			// Store final status code
			ctx.SetState("upstream_status", recorder.statusCode)

			// Track circuit breaker if available
			if recorder.statusCode >= 500 {
				p.recordUpstreamFailure(ctx)
			} else {
				p.recordUpstreamSuccess(ctx)
			}

			return lastErr
		}

		// Wait before retry (exponential backoff)
		if attempt < p.maxRetries {
			backoff := time.Duration(1<<uint(attempt)) * 100 * time.Millisecond
			time.Sleep(backoff)
		}
	}

	// All retries exhausted
	return fmt.Errorf("all retry attempts exhausted: %w", lastErr)
}

// createReverseProxy creates a configured reverse proxy
func (p *UpstreamProxyPlugin) createReverseProxy(upstream *url.URL) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.Transport = p.transport

	// Custom director to modify the request
	defaultDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		defaultDirector(req)

		// Add custom headers
		req.Header.Set("X-Gateway", "Iket")
		req.Header.Set("X-Gateway-Request-ID", req.Context().Value("request_id").(string))
	}

	// Modify response
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Add response headers
		resp.Header.Set("X-Gateway", "Iket")

		// Remove hop-by-hop headers
		p.removeHopByHopHeaders(resp.Header)

		return nil
	}

	return proxy
}

// prepareRequest prepares the request for proxying
func (p *UpstreamProxyPlugin) prepareRequest(r *http.Request, upstream *url.URL) {
	// Set scheme and host
	r.URL.Scheme = upstream.Scheme
	r.URL.Host = upstream.Host

	// Preserve or override host header
	if !p.preserveHost {
		r.Host = upstream.Host
	}

	// Strip path prefix if configured
	if p.stripPath {
		// This would need the route path prefix to strip
		// For now, we'll keep the original path
	}

	// Add X-Forwarded-* headers
	if p.addForwardedHeaders {
		p.addForwardedHeadersToRequest(r)
	}

	// Remove hop-by-hop headers
	p.removeHopByHopHeaders(r.Header)
}

// addForwardedHeaders adds X-Forwarded-* headers
func (p *UpstreamProxyPlugin) addForwardedHeadersToRequest(r *http.Request) {
	// X-Forwarded-For
	if clientIP, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if prior := r.Header.Get("X-Forwarded-For"); prior != "" {
			clientIP = prior + ", " + clientIP
		}
		r.Header.Set("X-Forwarded-For", clientIP)
	}

	// X-Forwarded-Proto
	if r.TLS != nil {
		r.Header.Set("X-Forwarded-Proto", "https")
	} else {
		r.Header.Set("X-Forwarded-Proto", "http")
	}

	// X-Forwarded-Host
	if r.Host != "" {
		r.Header.Set("X-Forwarded-Host", r.Host)
	}
}

// removeHopByHopHeaders removes headers that shouldn't be forwarded
func (p *UpstreamProxyPlugin) removeHopByHopHeaders(h http.Header) {
	hopByHopHeaders := []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailers",
		"Transfer-Encoding",
		"Upgrade",
	}

	for _, header := range hopByHopHeaders {
		h.Del(header)
	}
}

// shouldRetry determines if a request should be retried
func (p *UpstreamProxyPlugin) shouldRetry(statusCode int, attempt int) bool {
	if attempt >= p.maxRetries {
		return false
	}

	return p.retryableStatuses[statusCode]
}

// recordUpstreamSuccess records a successful upstream response
func (p *UpstreamProxyPlugin) recordUpstreamSuccess(ctx *engine.ExecutionContext) {
	// If circuit breaker is in context, record success
	if _, ok := ctx.GetState("circuit"); ok {
		// This would call the circuit breaker's record success method
		// For now, we'll just set a state
		ctx.SetState("upstream_success", true)
	}
}

// recordUpstreamFailure records a failed upstream response
func (p *UpstreamProxyPlugin) recordUpstreamFailure(ctx *engine.ExecutionContext) {
	// If circuit breaker is in context, record failure
	if _, ok := ctx.GetState("circuit"); ok {
		// This would call the circuit breaker's record failure method
		ctx.SetState("upstream_failure", true)
	}
}

// getUpstreamURL extracts the upstream URL from the route
func (p *UpstreamProxyPlugin) getUpstreamURL(route interface{}) string {
	// This would need to be adapted based on your CompiledRoute structure
	if routeMap, ok := route.(map[string]interface{}); ok {
		if upstream, ok := routeMap["upstream"].(string); ok {
			return upstream
		}
	}
	return ""
}

// initializeTransport sets up the HTTP transport
func (p *UpstreamProxyPlugin) initializeTransport() {
	p.transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          p.maxIdleConns,
		MaxIdleConnsPerHost:   p.maxIdleConnsPerHost,
		IdleConnTimeout:       p.idleConnTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: p.timeout,
	}
}

// responseRecorder captures the response status code
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Health check
func (p *UpstreamProxyPlugin) Health() error {
	if !p.enabled {
		return fmt.Errorf("upstream proxy plugin is disabled")
	}

	if p.transport == nil {
		return fmt.Errorf("transport not initialized")
	}

	return nil
}

// Close cleans up resources
func (p *UpstreamProxyPlugin) Close() error {
	if p.transport != nil {
		p.transport.CloseIdleConnections()
	}
	return nil
}

// GetStats returns statistics about the proxy
func (p *UpstreamProxyPlugin) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"max_idle_conns":          p.maxIdleConns,
		"max_idle_conns_per_host": p.maxIdleConnsPerHost,
		"idle_conn_timeout":       p.idleConnTimeout.String(),
		"max_retries":             p.maxRetries,
		"preserve_host":           p.preserveHost,
		"strip_path":              p.stripPath,
	}
}
