package config

import (
	"strings"
	"testing"
)

func TestNumouJWKSConfigurationRequiresTrustedBoundedRotationProfile(t *testing.T) {
	base := func() *Config {
		return &Config{Server: ServerConfig{Port: 8080}, Security: SecurityConfig{
			Jwt: JWTConfig{
				Enabled: true, NumouAccessTokens: true, Algorithms: []string{"RS256"},
				Issuer: "https://identity.example", Audience: "iket",
				JWKSURL: "https://identity.example/.well-known/jwks.json",
				JWKSCacheTTL: "5m", JWKSStaleTTL: "15m",
				JWKSRefreshTimeout: "2s", JWKSMaxKeys: 16,
			},
		}}
	}
	if err := NewConfigValidator().Validate(base()); err != nil {
		t.Fatalf("expected valid JWKS configuration, got %v", err)
	}
	cases := []struct {
		name string
		mutate func(*Config)
	}{
		{"requires HTTPS", func(c *Config) { c.Security.Jwt.JWKSURL = "http://identity.example/jwks" }},
		{"requires issuer host match", func(c *Config) { c.Security.Jwt.JWKSURL = "https://attacker.example/jwks" }},
		{"rejects static and remote key ambiguity", func(c *Config) { c.Security.Jwt.PublicKeyFile = "/keys/old.pem" }},
		{"requires one key source", func(c *Config) { c.Security.Jwt.JWKSURL = "" }},
		{"bounds cache TTL", func(c *Config) { c.Security.Jwt.JWKSCacheTTL = "10s" }},
		{"requires stale window at least cache TTL", func(c *Config) { c.Security.Jwt.JWKSStaleTTL = "1m" }},
		{"bounds stale window", func(c *Config) { c.Security.Jwt.JWKSStaleTTL = "2h" }},
		{"bounds refresh timeout", func(c *Config) { c.Security.Jwt.JWKSRefreshTimeout = "11s" }},
		{"bounds key count", func(c *Config) { c.Security.Jwt.JWKSMaxKeys = 33 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(cfg)
			if err := NewConfigValidator().Validate(cfg); err == nil || !strings.Contains(err.Error(), "security.jwt") {
				t.Fatalf("expected JWKS validation error, got %v", err)
			}
		})
	}
}
