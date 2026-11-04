package config

import (
	"strings"
	"testing"
)

func TestSecurityConfigRuleRequiresSecureBoundedNumouIntrospection(t *testing.T) {
	base := func() *Config {
		return &Config{Server: ServerConfig{Port: 8080}, Security: SecurityConfig{
			Jwt: JWTConfig{
				Enabled: true, NumouAccessTokens: true, PublicKeyFile: "/keys/numou.pub",
				Algorithms: []string{"RS256"}, Issuer: "https://identity.example", Audience: "iket",
				Introspection: NumouIntrospectionConfig{
					Enabled: true, URL: "https://identity.example/internal/v1/oauth2/introspect",
					CAFile: "/tls/ca.pem", ClientCertFile: "/tls/iket.crt", ClientKeyFile: "/tls/iket.key",
					Timeout: "2s", PositiveCacheTTL: "5s", NegativeCacheTTL: "1s", CacheMaxEntries: 1000,
				},
			},
		}}
	}
	if err := NewConfigValidator().Validate(base()); err != nil {
		t.Fatalf("expected valid introspection config, got %v", err)
	}
	tests := []struct{name string; mutate func(*Config)}{
		{"requires Numou profile", func(c *Config) { c.Security.Jwt.NumouAccessTokens = false }},
		{"requires HTTPS", func(c *Config) { c.Security.Jwt.Introspection.URL = "http://identity.example/introspect" }},
		{"requires mTLS client key", func(c *Config) { c.Security.Jwt.Introspection.ClientKeyFile = "" }},
		{"caps positive cache", func(c *Config) { c.Security.Jwt.Introspection.PositiveCacheTTL = "6s" }},
		{"caps negative cache", func(c *Config) { c.Security.Jwt.Introspection.NegativeCacheTTL = "2s" }},
		{"bounds cache capacity", func(c *Config) { c.Security.Jwt.Introspection.CacheMaxEntries = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(cfg)
			if err := NewConfigValidator().Validate(cfg); err == nil || !strings.Contains(err.Error(), "introspection") {
				t.Fatalf("expected introspection validation error, got %v", err)
			}
		})
	}
}
