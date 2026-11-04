package gateway

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bhangun/iket/pkg/config"
	"github.com/bhangun/iket/pkg/core/authcontext"
	"github.com/bhangun/iket/pkg/logging"
	"github.com/golang-jwt/jwt/v4"
)

func TestNumouJWTMiddlewareUsesVerifiedTenantAndApplicationClaims(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil { t.Fatalf("generate RSA key: %v", err) }
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil { t.Fatalf("marshal RSA public key: %v", err) }
	keyPath := filepath.Join(t.TempDir(), "numou-public.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": "https://identity.numou.internal",
		"sub": "nmc_client_1234567890",
		"aud": []string{"numou-iket"},
		"client_id": "nmc_client_1234567890",
		"tenant_id": "8d5d8a7e-6d6b-4f0a-9d1f-7cfa8f35c2b1",
		"application_id": "b2b1b84d-23d8-4a9b-8b42-4f2e7f2d6c11",
		"credential_version": float64(1),
		"token_use": "access",
		"scope": "orders.read",
		"iat": now.Unix(),
		"exp": now.Add(5*time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(privateKey)
	if err != nil { t.Fatalf("sign token: %v", err) }

	gateway := &Gateway{config: &config.Config{}, logger: logging.NewLogger(false)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	req.Header.Set("X-Tenant-ID", "attacker-tenant")
	req.Header.Set("X-Application-ID", "attacker-app")
	resp := httptest.NewRecorder()

	gateway.jwtAuthMiddleware(config.JWTConfig{
		Enabled: true, Algorithms: []string{"RS256"}, PublicKeyFile: keyPath,
		NumouAccessTokens: true, Issuer: "https://identity.numou.internal", Audience: "numou-iket",
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Tenant-ID"); got != claims["tenant_id"] {
			t.Fatalf("tenant header was not replaced by verified claim: %q", got)
		}
		if got := r.Header.Get("X-Application-ID"); got != claims["application_id"] {
			t.Fatalf("application header was not replaced by verified claim: %q", got)
		}
		principal, ok := authcontext.PrincipalFromContext(r.Context())
		if !ok || principal.TenantID != claims["tenant_id"] || principal.ApplicationID != claims["application_id"] {
			t.Fatalf("expected verified machine principal, got %+v ok=%v", principal, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected valid Numou token, got %d: %s", resp.Code, resp.Body.String())
	}
}
