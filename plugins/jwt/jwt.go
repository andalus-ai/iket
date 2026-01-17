package jwt

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
	"github.com/golang-jwt/jwt/v4"
)

// JWTPlugin provides JWT authentication and validation
type JWTPlugin struct {
	// Base plugin properties
	name     string
	priority int
	timeout  time.Duration
	failure  engine.FailureMode

	// JWT configuration
	enabled    bool
	secret     string
	publicKey  *rsa.PublicKey
	algorithms []string
	issuer     string
	audience   string
	skipPaths  []string

	// Token extraction
	headerName string
	queryParam string
	cookieName string

	// Claims validation
	requireClaims []string

	// Cache for performance
	validatedTokens sync.Map // token -> claims (with TTL)

	logger *logging.Logger
	mu     sync.RWMutex
}

// Claims represents JWT claims with standard and custom fields
type Claims struct {
	UserID   string   `json:"user_id,omitempty"`
	Username string   `json:"username,omitempty"`
	Email    string   `json:"email,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Scope    string   `json:"scope,omitempty"`
	jwt.RegisteredClaims
}

// NewJWTPlugin creates a new JWT authentication plugin
func NewJWTPlugin(logger *logging.Logger) *JWTPlugin {
	return &JWTPlugin{
		name:       "jwt",
		priority:   100, // High priority for auth
		timeout:    5 * time.Second,
		failure:    engine.FailureModeDeny,
		algorithms: []string{"HS256", "RS256"},
		headerName: "Authorization",
		logger:     logger,
	}
}

// Plugin interface implementation

func (p *JWTPlugin) Name() string {
	return p.name
}

func (p *JWTPlugin) Phase() engine.Phase {
	return engine.PhaseAuth
}

func (p *JWTPlugin) Priority() int {
	return p.priority
}

func (p *JWTPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *JWTPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *JWTPlugin) Initialize(config map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Enabled flag
	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	}

	if !p.enabled {
		return nil
	}

	// Secret for HS256
	if secret, ok := config["secret"].(string); ok {
		p.secret = secret
	}

	// Public key file for RS256
	if publicKeyFile, ok := config["public_key_file"].(string); ok && publicKeyFile != "" {
		if err := p.loadPublicKey(publicKeyFile); err != nil {
			return fmt.Errorf("failed to load public key: %w", err)
		}
	}

	// Algorithms
	if algorithms, ok := config["algorithms"].([]interface{}); ok {
		p.algorithms = make([]string, len(algorithms))
		for i, alg := range algorithms {
			p.algorithms[i] = alg.(string)
		}
	}

	// Issuer validation
	if issuer, ok := config["issuer"].(string); ok {
		p.issuer = issuer
	}

	// Audience validation
	if audience, ok := config["audience"].(string); ok {
		p.audience = audience
	}

	// Skip paths
	if skipPaths, ok := config["skip_paths"].([]interface{}); ok {
		p.skipPaths = make([]string, len(skipPaths))
		for i, path := range skipPaths {
			p.skipPaths[i] = path.(string)
		}
	}

	// Token extraction config
	if headerName, ok := config["header_name"].(string); ok {
		p.headerName = headerName
	}
	if queryParam, ok := config["query_param"].(string); ok {
		p.queryParam = queryParam
	}
	if cookieName, ok := config["cookie_name"].(string); ok {
		p.cookieName = cookieName
	}

	// Required claims
	if reqClaims, ok := config["require_claims"].([]interface{}); ok {
		p.requireClaims = make([]string, len(reqClaims))
		for i, claim := range reqClaims {
			p.requireClaims[i] = claim.(string)
		}
	}

	// Validate configuration
	if err := p.validateConfig(); err != nil {
		return err
	}

	p.logger.Info("JWT plugin initialized",
		logging.String("algorithms", strings.Join(p.algorithms, ",")),
		logging.Bool("has_secret", p.secret != ""),
		logging.Bool("has_public_key", p.publicKey != nil),
	)

	return nil
}

func (p *JWTPlugin) Execute(ctx *engine.ExecutionContext) error {
	p.mu.RLock()
	enabled := p.enabled
	p.mu.RUnlock()

	if !enabled {
		return nil
	}

	// Extract request from context
	req, ok := ctx.GetState("http_request")
	if !ok {
		return fmt.Errorf("http request not found in context")
	}
	httpReq := req.(*http.Request)

	// Check if path should skip JWT validation
	if p.shouldSkipPath(httpReq.URL.Path) {
		ctx.SetState("jwt_skipped", true)
		p.logger.Debug("JWT validation skipped",
			logging.String("path", httpReq.URL.Path),
			logging.String("request_id", ctx.RequestID),
		)
		return nil
	}

	// Extract token
	tokenString, err := p.extractToken(httpReq)
	if err != nil {
		p.logger.Warn("Token extraction failed",
			logging.Error(err),
			logging.String("request_id", ctx.RequestID),
		)
		return fmt.Errorf("missing or invalid token: %w", err)
	}

	// Check cache first
	if cached, ok := p.validatedTokens.Load(tokenString); ok {
		cachedClaims := cached.(*CachedClaims)
		if time.Now().Before(cachedClaims.ExpiresAt) {
			ctx.SetState("jwt_claims", cachedClaims.Claims)
			ctx.SetState("jwt_validated", true)
			return nil
		}
		// Cache expired
		p.validatedTokens.Delete(tokenString)
	}

	// Parse and validate token
	claims, err := p.validateToken(tokenString)
	if err != nil {
		p.logger.Warn("Token validation failed",
			logging.Error(err),
			logging.String("request_id", ctx.RequestID),
		)
		return fmt.Errorf("invalid token: %w", err)
	}

	// Validate required claims
	if err := p.validateRequiredClaims(claims); err != nil {
		return fmt.Errorf("required claims missing: %w", err)
	}

	// Cache valid token (for 1 minute)
	p.validatedTokens.Store(tokenString, &CachedClaims{
		Claims:    claims,
		ExpiresAt: time.Now().Add(1 * time.Minute),
	})

	// Store claims in context
	ctx.SetState("jwt_claims", claims)
	ctx.SetState("jwt_validated", true)
	ctx.SetState("user_id", claims.UserID)
	ctx.SetState("username", claims.Username)
	ctx.SetState("roles", claims.Roles)

	p.logger.Debug("JWT validation successful",
		logging.String("user_id", claims.UserID),
		logging.String("request_id", ctx.RequestID),
	)

	return nil
}

// Token extraction

func (p *JWTPlugin) extractToken(r *http.Request) (string, error) {
	// Try header first
	if p.headerName != "" {
		if token := p.extractFromHeader(r); token != "" {
			return token, nil
		}
	}

	// Try query parameter
	if p.queryParam != "" {
		if token := r.URL.Query().Get(p.queryParam); token != "" {
			return token, nil
		}
	}

	// Try cookie
	if p.cookieName != "" {
		if cookie, err := r.Cookie(p.cookieName); err == nil {
			return cookie.Value, nil
		}
	}

	return "", fmt.Errorf("token not found in header, query, or cookie")
}

func (p *JWTPlugin) extractFromHeader(r *http.Request) string {
	authHeader := r.Header.Get(p.headerName)
	if authHeader == "" {
		return ""
	}

	// Support "Bearer <token>" format
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}

	return authHeader
}

// Token validation

func (p *JWTPlugin) validateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, p.keyFunc())
	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("token is not valid")
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}

	// Validate issuer if configured
	if p.issuer != "" && claims.Issuer != p.issuer {
		return nil, fmt.Errorf("invalid issuer: expected %s, got %s", p.issuer, claims.Issuer)
	}

	// Validate audience if configured
	if p.audience != "" {
		validAudience := false
		for _, aud := range claims.Audience {
			if aud == p.audience {
				validAudience = true
				break
			}
		}
		if !validAudience {
			return nil, fmt.Errorf("invalid audience: %s not found", p.audience)
		}
	}

	return claims, nil
}

func (p *JWTPlugin) keyFunc() jwt.Keyfunc {
	return func(token *jwt.Token) (interface{}, error) {
		// Validate algorithm
		alg := token.Method.Alg()
		if !p.isAlgorithmAllowed(alg) {
			return nil, fmt.Errorf("unsupported algorithm: %s", alg)
		}

		// Return appropriate key based on algorithm
		switch alg {
		case "HS256", "HS384", "HS512":
			if p.secret == "" {
				return nil, fmt.Errorf("secret key required for HMAC algorithms")
			}
			return []byte(p.secret), nil

		case "RS256", "RS384", "RS512":
			if p.publicKey == nil {
				return nil, fmt.Errorf("public key required for RSA algorithms")
			}
			return p.publicKey, nil

		default:
			return nil, fmt.Errorf("unsupported algorithm: %s", alg)
		}
	}
}

func (p *JWTPlugin) isAlgorithmAllowed(alg string) bool {
	for _, allowed := range p.algorithms {
		if allowed == alg {
			return true
		}
	}
	return false
}

func (p *JWTPlugin) validateRequiredClaims(claims *Claims) error {
	if len(p.requireClaims) == 0 {
		return nil
	}

	claimsMap := p.claimsToMap(claims)

	for _, requiredClaim := range p.requireClaims {
		if _, exists := claimsMap[requiredClaim]; !exists {
			return fmt.Errorf("required claim missing: %s", requiredClaim)
		}
	}

	return nil
}

func (p *JWTPlugin) claimsToMap(claims *Claims) map[string]interface{} {
	return map[string]interface{}{
		"sub":      claims.Subject,
		"iss":      claims.Issuer,
		"aud":      claims.Audience,
		"exp":      claims.ExpiresAt,
		"nbf":      claims.NotBefore,
		"iat":      claims.IssuedAt,
		"user_id":  claims.UserID,
		"username": claims.Username,
		"email":    claims.Email,
		"roles":    claims.Roles,
		"scope":    claims.Scope,
	}
}

// Helper functions

func (p *JWTPlugin) shouldSkipPath(path string) bool {
	for _, skipPath := range p.skipPaths {
		if strings.HasPrefix(path, skipPath) {
			return true
		}
	}
	return false
}

func (p *JWTPlugin) loadPublicKey(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read public key file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("failed to decode PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("public key is not RSA")
	}

	p.publicKey = rsaPub
	return nil
}

func (p *JWTPlugin) validateConfig() error {
	if !p.enabled {
		return nil
	}

	// Must have either secret or public key
	hasHS := false
	hasRS := false
	for _, alg := range p.algorithms {
		if strings.HasPrefix(alg, "HS") {
			hasHS = true
		}
		if strings.HasPrefix(alg, "RS") {
			hasRS = true
		}
	}

	if hasHS && p.secret == "" {
		return fmt.Errorf("secret is required for HMAC algorithms")
	}

	if hasRS && p.publicKey == nil {
		return fmt.Errorf("public key is required for RSA algorithms")
	}

	return nil
}

// CachedClaims holds validated claims with expiration
type CachedClaims struct {
	Claims    *Claims
	ExpiresAt time.Time
}

// Health check
func (p *JWTPlugin) Health() error {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if !p.enabled {
		return fmt.Errorf("JWT plugin is disabled")
	}

	if p.secret == "" && p.publicKey == nil {
		return fmt.Errorf("neither secret nor public key configured")
	}

	return nil
}

// GetClaimsFromContext is a helper to extract JWT claims from execution context
func GetClaimsFromContext(ctx *engine.ExecutionContext) (*Claims, bool) {
	claims, ok := ctx.GetState("jwt_claims")
	if !ok {
		return nil, false
	}

	jwtClaims, ok := claims.(*Claims)
	return jwtClaims, ok
}

// HasRole checks if the authenticated user has a specific role
func HasRole(ctx *engine.ExecutionContext, role string) bool {
	claims, ok := GetClaimsFromContext(ctx)
	if !ok {
		return false
	}

	for _, r := range claims.Roles {
		if r == role {
			return true
		}
	}

	return false
}

// RequireRoles creates a validation function that checks for required roles
func RequireRoles(requiredRoles []string) func(*engine.ExecutionContext) error {
	return func(ctx *engine.ExecutionContext) error {
		claims, ok := GetClaimsFromContext(ctx)
		if !ok {
			return fmt.Errorf("no JWT claims found")
		}

		if len(claims.Roles) == 0 {
			return fmt.Errorf("no roles in token")
		}

		for _, required := range requiredRoles {
			hasRole := false
			for _, actual := range claims.Roles {
				if required == actual {
					hasRole = true
					break
				}
			}
			if !hasRole {
				return fmt.Errorf("missing required role: %s", required)
			}
		}

		return nil
	}
}
