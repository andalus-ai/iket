package opa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
)

// OPAPlugin implements Open Policy Agent integration
type OPAPlugin struct {
	// Base plugin properties
	name     string
	priority int
	timeout  time.Duration
	failure  engine.FailureMode

	// OPA configuration
	enabled    bool
	serverURL  string
	policyPath string
	bundleURL  string

	// Authentication
	authToken string

	// Caching
	enableCache bool
	cacheSize   int
	cacheTTL    time.Duration
	cache       sync.Map // decision cache

	// Input building
	includeBody    bool
	includeHeaders bool

	// HTTP client
	client *http.Client

	logger *logging.Logger
	mu     sync.RWMutex
}

// OPAInput represents the input sent to OPA
type OPAInput struct {
	User    *UserContext           `json:"user,omitempty"`
	Request *RequestContext        `json:"request"`
	Route   *RouteContext          `json:"route"`
	Custom  map[string]interface{} `json:"custom,omitempty"`
}

// UserContext contains authenticated user information
type UserContext struct {
	ID       string                 `json:"id,omitempty"`
	Username string                 `json:"username,omitempty"`
	Email    string                 `json:"email,omitempty"`
	Roles    []string               `json:"roles,omitempty"`
	Claims   map[string]interface{} `json:"claims,omitempty"`
}

// RequestContext contains HTTP request information
type RequestContext struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   map[string]string `json:"query,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    interface{}       `json:"body,omitempty"`
	IP      string            `json:"ip"`
}

// RouteContext contains route information
type RouteContext struct {
	ID       string                 `json:"id"`
	Path     string                 `json:"path"`
	Upstream string                 `json:"upstream,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// OPARequest represents the request sent to OPA
type OPARequest struct {
	Input OPAInput `json:"input"`
}

// OPAResponse represents the response from OPA
type OPAResponse struct {
	DecisionID string                 `json:"decision_id,omitempty"`
	Result     interface{}            `json:"result"`
	Metrics    map[string]interface{} `json:"metrics,omitempty"`
}

// PolicyDecision represents the parsed policy decision
type PolicyDecision struct {
	Allow   bool                   `json:"allow"`
	Deny    bool                   `json:"deny"`
	Reason  string                 `json:"reason,omitempty"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// NewOPAPlugin creates a new OPA policy plugin
func NewOPAPlugin(logger *logging.Logger) *OPAPlugin {
	return &OPAPlugin{
		name:           "opa_policy",
		priority:       90,
		timeout:        3 * time.Second,
		failure:        engine.FailureModeDeny,
		enableCache:    true,
		cacheSize:      1000,
		cacheTTL:       5 * time.Minute,
		includeBody:    false,
		includeHeaders: false,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		logger: logger,
	}
}

// Plugin interface implementation

func (p *OPAPlugin) Name() string {
	return p.name
}

func (p *OPAPlugin) Phase() engine.Phase {
	return engine.PhasePolicy
}

func (p *OPAPlugin) Priority() int {
	return p.priority
}

func (p *OPAPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *OPAPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *OPAPlugin) Initialize(config map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Enabled flag
	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	}

	if !p.enabled {
		return nil
	}

	// OPA server URL
	if serverURL, ok := config["server_url"].(string); ok {
		p.serverURL = serverURL
	}

	// Policy path (e.g., "data/example/allow")
	if policyPath, ok := config["policy_path"].(string); ok {
		p.policyPath = policyPath
	}

	// Bundle URL
	if bundleURL, ok := config["bundle_url"].(string); ok {
		p.bundleURL = bundleURL
	}

	// Authentication token
	if authToken, ok := config["auth_token"].(string); ok {
		p.authToken = authToken
	}

	// Cache configuration
	if enableCache, ok := config["enable_cache"].(bool); ok {
		p.enableCache = enableCache
	}

	if cacheTTL, ok := config["cache_ttl"].(float64); ok {
		p.cacheTTL = time.Duration(cacheTTL) * time.Second
	}

	// Input configuration
	if includeBody, ok := config["include_body"].(bool); ok {
		p.includeBody = includeBody
	}

	if includeHeaders, ok := config["include_headers"].(bool); ok {
		p.includeHeaders = includeHeaders
	}

	// Timeout
	if timeout, ok := config["timeout"].(float64); ok {
		p.timeout = time.Duration(timeout) * time.Second
		p.client.Timeout = p.timeout
	}

	// Validate configuration
	if p.serverURL == "" {
		return fmt.Errorf("server_url is required")
	}

	if p.policyPath == "" {
		return fmt.Errorf("policy_path is required")
	}

	p.logger.Info("OPA plugin initialized",
		logging.String("server_url", p.serverURL),
		logging.String("policy_path", p.policyPath),
		logging.Bool("cache_enabled", p.enableCache),
	)

	return nil
}

func (p *OPAPlugin) Execute(ctx *engine.ExecutionContext) error {
	if !p.enabled {
		return nil
	}

	// Build OPA input
	input, err := p.buildInput(ctx)
	if err != nil {
		return fmt.Errorf("failed to build OPA input: %w", err)
	}

	// Check cache if enabled
	if p.enableCache {
		cacheKey := p.getCacheKey(input)
		if cached, ok := p.cache.Load(cacheKey); ok {
			cachedDecision := cached.(*CachedDecision)
			if time.Now().Before(cachedDecision.ExpiresAt) {
				p.logger.Debug("OPA decision from cache",
					logging.String("request_id", ctx.RequestID),
					logging.Bool("allow", cachedDecision.Decision.Allow),
				)

				return p.handleDecision(ctx, cachedDecision.Decision)
			}
			// Cache expired
			p.cache.Delete(cacheKey)
		}
	}

	// Evaluate policy
	decision, err := p.evaluatePolicy(ctx.Context, input)
	if err != nil {
		p.logger.Error("OPA policy evaluation failed",
			err,
			logging.String("request_id", ctx.RequestID),
		)
		return fmt.Errorf("policy evaluation failed: %w", err)
	}

	// Cache decision if enabled
	if p.enableCache {
		cacheKey := p.getCacheKey(input)
		p.cache.Store(cacheKey, &CachedDecision{
			Decision:  decision,
			ExpiresAt: time.Now().Add(p.cacheTTL),
		})
	}

	p.logger.Debug("OPA policy evaluated",
		logging.String("request_id", ctx.RequestID),
		logging.Bool("allow", decision.Allow),
		logging.String("reason", decision.Reason),
	)

	return p.handleDecision(ctx, decision)
}

// buildInput constructs the OPA input from execution context
func (p *OPAPlugin) buildInput(ctx *engine.ExecutionContext) (*OPAInput, error) {
	input := &OPAInput{
		Route: &RouteContext{
			ID: ctx.RouteID,
		},
	}

	// Extract HTTP request
	req, ok := ctx.GetState("http_request")
	if !ok {
		return nil, fmt.Errorf("http request not found in context")
	}
	httpReq := req.(*http.Request)

	// Build request context
	input.Request = &RequestContext{
		Method: httpReq.Method,
		Path:   httpReq.URL.Path,
		IP:     ctx.ClientIP,
	}

	// Add query parameters
	if len(httpReq.URL.Query()) > 0 {
		input.Request.Query = make(map[string]string)
		for key, values := range httpReq.URL.Query() {
			if len(values) > 0 {
				input.Request.Query[key] = values[0]
			}
		}
	}

	// Add headers if configured
	if p.includeHeaders {
		input.Request.Headers = make(map[string]string)
		for key, values := range httpReq.Header {
			if len(values) > 0 {
				input.Request.Headers[key] = values[0]
			}
		}
	}

	// Add body if configured
	if p.includeBody && httpReq.Body != nil {
		// Note: In production, you might want to limit body size
		// and handle different content types
		bodyBytes, err := io.ReadAll(httpReq.Body)
		if err == nil && len(bodyBytes) > 0 {
			// Try to parse as JSON
			var bodyJSON interface{}
			if err := json.Unmarshal(bodyBytes, &bodyJSON); err == nil {
				input.Request.Body = bodyJSON
			}
		}
		// Restore body for downstream
		httpReq.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	// Extract user context from JWT claims
	if claims, ok := ctx.GetState("jwt_claims"); ok {
		input.User = &UserContext{}

		if claimsMap, ok := claims.(map[string]interface{}); ok {
			if userID, ok := claimsMap["user_id"].(string); ok {
				input.User.ID = userID
			}
			if username, ok := claimsMap["username"].(string); ok {
				input.User.Username = username
			}
			if email, ok := claimsMap["email"].(string); ok {
				input.User.Email = email
			}
			if roles, ok := claimsMap["roles"].([]string); ok {
				input.User.Roles = roles
			}

			input.User.Claims = claimsMap
		}
	}

	return input, nil
}

// evaluatePolicy sends the input to OPA and gets the decision
func (p *OPAPlugin) evaluatePolicy(ctx context.Context, input *OPAInput) (*PolicyDecision, error) {
	// Construct OPA URL
	url := fmt.Sprintf("%s/v1/%s", p.serverURL, p.policyPath)

	// Prepare request
	requestBody := &OPARequest{Input: *input}
	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OPA request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create OPA request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Add authentication if configured
	if p.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.authToken)
	}

	// Send request
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OPA request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OPA returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse response
	var opaResp OPAResponse
	if err := json.NewDecoder(resp.Body).Decode(&opaResp); err != nil {
		return nil, fmt.Errorf("failed to parse OPA response: %w", err)
	}

	// Parse decision from result
	decision, err := p.parseDecision(opaResp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OPA decision: %w", err)
	}

	return decision, nil
}

// parseDecision interprets the OPA result as a policy decision
func (p *OPAPlugin) parseDecision(result interface{}) (*PolicyDecision, error) {
	decision := &PolicyDecision{
		Details: make(map[string]interface{}),
	}

	switch v := result.(type) {
	case bool:
		// Simple boolean result
		decision.Allow = v
		decision.Deny = !v

	case map[string]interface{}:
		// Structured result
		if allow, ok := v["allow"].(bool); ok {
			decision.Allow = allow
		}
		if deny, ok := v["deny"].(bool); ok {
			decision.Deny = deny
		}
		if reason, ok := v["reason"].(string); ok {
			decision.Reason = reason
		}

		decision.Details = v

	default:
		return nil, fmt.Errorf("unexpected OPA result type: %T", result)
	}

	// If deny is not explicitly set, infer from allow
	if !decision.Deny && !decision.Allow {
		decision.Deny = true
	}

	return decision, nil
}

// handleDecision processes the policy decision
func (p *OPAPlugin) handleDecision(ctx *engine.ExecutionContext, decision *PolicyDecision) error {
	// Store decision in context
	ctx.SetState("opa_decision", decision)
	ctx.SetState("policy_allowed", decision.Allow)

	if decision.Deny || !decision.Allow {
		reason := decision.Reason
		if reason == "" {
			reason = "policy denied request"
		}

		return fmt.Errorf("policy denied: %s", reason)
	}

	return nil
}

// getCacheKey generates a cache key from the input
func (p *OPAPlugin) getCacheKey(input *OPAInput) string {
	// Simple cache key based on user, method, and path
	userID := ""
	if input.User != nil {
		userID = input.User.ID
	}

	return fmt.Sprintf("%s:%s:%s", userID, input.Request.Method, input.Request.Path)
}

// CachedDecision holds a cached policy decision
type CachedDecision struct {
	Decision  *PolicyDecision
	ExpiresAt time.Time
}

// Health check
func (p *OPAPlugin) Health() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if !p.enabled {
		return fmt.Errorf("OPA plugin is disabled")
	}

	// Try to reach OPA server
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	healthURL := fmt.Sprintf("%s/health", p.serverURL)
	req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("OPA server unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OPA health check failed with status %d", resp.StatusCode)
	}

	return nil
}

// GetStats returns statistics about policy evaluations
func (p *OPAPlugin) GetStats() map[string]interface{} {
	cacheSize := 0
	p.cache.Range(func(key, value interface{}) bool {
		cacheSize++
		return true
	})

	return map[string]interface{}{
		"cache_size":    cacheSize,
		"cache_enabled": p.enableCache,
		"server_url":    p.serverURL,
		"policy_path":   p.policyPath,
	}
}

// ClearCache removes all cached decisions
func (p *OPAPlugin) ClearCache() {
	p.cache.Range(func(key, value interface{}) bool {
		p.cache.Delete(key)
		return true
	})

	p.logger.Info("OPA cache cleared")
}
