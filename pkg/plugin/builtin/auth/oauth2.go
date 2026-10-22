package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
)

// OAuth2Plugin implements token introspection authentication
type OAuth2Plugin struct {
	name          string
	priority      int
	timeout       time.Duration
	failure       engine.FailureMode
	enabled       bool
	introspectURL string
	clientID      string
	clientSecret  string
	skipPaths     []string
	claimsContext string
	httpClient    *http.Client
	mu            sync.RWMutex
	logger        *logging.Logger
}

// IntrospectionResponse represents the response from an OAuth2 introspection endpoint
type IntrospectionResponse struct {
	Active    bool     `json:"active"`
	Scope     string   `json:"scope"`
	ClientID  string   `json:"client_id"`
	Username  string   `json:"username"`
	TokenType string   `json:"token_type"`
	Exp       int64    `json:"exp"`
	Iat       int64    `json:"iat"`
	Sub       string   `json:"sub"`
	Aud       []string `json:"aud"`
	Iss       string   `json:"iss"`
}

// Claims contains user information extracted from the introspection response
type OAuth2Claims struct {
	UserID   string            `json:"user_id"`
	Username string            `json:"username"`
	Email    string            `json:"email"`
	Roles    []string          `json:"roles"`
	Scope    string            `json:"scope"`
	ClientID string            `json:"client_id"`
	Custom   map[string]string `json:"custom,omitempty"`
}

// NewOAuth2Plugin creates a new OAuth2 plugin
func NewOAuth2Plugin(logger *logging.Logger) *OAuth2Plugin {
	return &OAuth2Plugin{
		name:          "oauth2",
		priority:      90,
		timeout:       5 * time.Second,
		failure:       engine.FailureModeDeny,
		claimsContext: "oauth2_claims",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

func (p *OAuth2Plugin) Name() string { return p.name }

func (p *OAuth2Plugin) Phase() engine.Phase {
	return engine.PhaseAuth
}

func (p *OAuth2Plugin) Priority() int {
	return p.priority
}

func (p *OAuth2Plugin) Timeout() time.Duration {
	return p.timeout
}

func (p *OAuth2Plugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *OAuth2Plugin) Initialize(config map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	}

	if !p.enabled {
		return nil
	}

	if introspectURL, ok := config["introspect_url"].(string); ok {
		p.introspectURL = introspectURL
	}

	if clientID, ok := config["client_id"].(string); ok {
		p.clientID = clientID
	}

	if clientSecret, ok := config["client_secret"].(string); ok {
		p.clientSecret = clientSecret
	}

	if skipPaths, ok := config["skip_paths"].([]interface{}); ok {
		for _, path := range skipPaths {
			if pathStr, ok := path.(string); ok {
				p.skipPaths = append(p.skipPaths, pathStr)
			}
		}
	}

	if claimsContext, ok := config["claims_context"].(string); ok {
		p.claimsContext = claimsContext
	}

	// Validate required configuration
	if p.introspectURL == "" {
		return fmt.Errorf("introspect_url is required for OAuth2 plugin")
	}

	if p.clientID == "" || p.clientSecret == "" {
		return fmt.Errorf("client_id and client_secret are required for OAuth2 plugin")
	}

	p.logger.Info("OAuth2 plugin initialized",
		logging.String("introspect_url", p.introspectURL),
		logging.String("client_id", p.clientID),
	)
	return nil
}

func (p *OAuth2Plugin) Execute(ctx *engine.ExecutionContext) error {
	if !p.enabled {
		return nil
	}

	// Extract HTTP request
	reqVal, ok := ctx.GetState("http_request")
	if !ok {
		return fmt.Errorf("http request not found in context")
	}
	r := reqVal.(*http.Request)

	// Skip OAuth2 validation for certain paths
	if p.shouldSkipValidation(r.URL.Path) {
		return nil
	}

	// Extract and validate OAuth2 token
	claims, err := p.validateToken(r)
	if err != nil {
		p.logger.Warn("OAuth2 validation failed",
			logging.Error(err),
			logging.String("path", r.URL.Path),
			logging.String("request_id", ctx.RequestID),
		)
		return err
	}

	// Add claims to execution context state
	ctx.SetState(p.claimsContext, claims)

	return nil
}

func (p *OAuth2Plugin) shouldSkipValidation(path string) bool {
	for _, skipPath := range p.skipPaths {
		if strings.HasPrefix(path, skipPath) {
			return true
		}
	}
	return false
}

func (p *OAuth2Plugin) validateToken(r *http.Request) (*OAuth2Claims, error) {
	// Extract token from Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, fmt.Errorf("missing authorization header")
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, fmt.Errorf("invalid authorization header format")
	}

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	// Introspect token with OAuth2 server
	introspection, err := p.introspectToken(tokenString)
	if err != nil {
		return nil, fmt.Errorf("failed to introspect token: %w", err)
	}

	if !introspection.Active {
		return nil, fmt.Errorf("token is not active")
	}

	// Convert introspection response to claims
	claims := &OAuth2Claims{
		UserID:   introspection.Sub,
		Username: introspection.Username,
		Scope:    introspection.Scope,
		ClientID: introspection.ClientID,
	}

	// Parse scope into roles
	if introspection.Scope != "" {
		claims.Roles = strings.Split(introspection.Scope, " ")
	}

	return claims, nil
}

func (p *OAuth2Plugin) introspectToken(token string) (*IntrospectionResponse, error) {
	// Prepare form data
	data := url.Values{}
	data.Set("token", token)
	data.Set("token_type_hint", "access_token")

	// Create request
	req, err := http.NewRequest("POST", p.introspectURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(p.clientID, p.clientSecret)

	// Make request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("introspection server returned status: %d", resp.StatusCode)
	}

	// Parse response
	var introspection IntrospectionResponse
	if err := json.NewDecoder(resp.Body).Decode(&introspection); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &introspection, nil
}
