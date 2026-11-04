package gateway

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

func validNumouClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": "https://identity.numou.internal",
		"sub": "nmc_client_1234567890",
		"aud": []interface{}{"numou-iket"},
		"client_id": "nmc_client_1234567890",
		"tenant_id": "8d5d8a7e-6d6b-4f0a-9d1f-7cfa8f35c2b1",
		"application_id": "b2b1b84d-23d8-4a9b-8b42-4f2e7f2d6c11",
		"credential_version": float64(1),
		"token_use": "access",
		"scope": "orders.read orders.write",
		"exp": float64(now.Add(5*time.Minute).Unix()),
	}
}

func TestValidateNumouAccessTokenClaimsAcceptsValidMachineToken(t *testing.T) {
	now := time.Now()
	if err := validateNumouAccessTokenClaims(validNumouClaims(now), "https://identity.numou.internal", "numou-iket", now); err != nil {
		t.Fatalf("expected valid Numou token, got %v", err)
	}
}

func TestValidateNumouAccessTokenClaimsRejectsIssuerAudienceAndTokenUseMismatch(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		mutate func(jwt.MapClaims)
	}{
		{"issuer", func(c jwt.MapClaims) { c["iss"] = "https://attacker.invalid" }},
		{"audience", func(c jwt.MapClaims) { c["aud"] = []interface{}{"another-api"} }},
		{"token use", func(c jwt.MapClaims) { c["token_use"] = "refresh" }},
		{"subject", func(c jwt.MapClaims) { c["sub"] = "different-client" }},
		{"missing expiry", func(c jwt.MapClaims) { delete(c, "exp") }},
		{"expired", func(c jwt.MapClaims) { c["exp"] = float64(now.Add(-time.Second).Unix()) }},
		{"tenant", func(c jwt.MapClaims) { c["tenant_id"] = "tenant-from-header" }},
		{"application", func(c jwt.MapClaims) { c["application_id"] = "not-a-uuid" }},
		{"version", func(c jwt.MapClaims) { c["credential_version"] = float64(0) }},
		{"scope", func(c jwt.MapClaims) { c["scope"] = "Orders.Read" }},
		{"empty scope", func(c jwt.MapClaims) { c["scope"] = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := validNumouClaims(now)
			tc.mutate(claims)
			if err := validateNumouAccessTokenClaims(claims, "https://identity.numou.internal", "numou-iket", now); err == nil {
				t.Fatal("expected invalid Numou token claims to be rejected")
			}
		})
	}
}

func TestValidateNumouAccessTokenClaimsRequiresConfiguredTrust(t *testing.T) {
	now := time.Now()
	if err := validateNumouAccessTokenClaims(validNumouClaims(now), "", "numou-iket", now); err == nil {
		t.Fatal("expected missing issuer configuration to fail")
	}
	if err := validateNumouAccessTokenClaims(validNumouClaims(now), "https://identity.numou.internal", "", now); err == nil {
		t.Fatal("expected missing audience configuration to fail")
	}
}
