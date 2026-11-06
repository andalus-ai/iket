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
	"github.com/bhangun/iket/pkg/logging"
	"github.com/golang-jwt/jwt/v4"
)

func TestNumouJWTMiddlewareVerifiesTokenUsingHTTPSJWKS(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	body := mustJWKSJSON(t, jwkForTest("active-2026-11", &privateKey.PublicKey))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	dir := t.TempDir()
	caFile := filepath.Join(dir, "identity-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caFile, caPEM, 0600); err != nil {
		t.Fatalf("write JWKS CA: %v", err)
	}

	cfg := config.JWTConfig{
		Enabled: true, NumouAccessTokens: true, Algorithms: []string{"RS256"},
		JWKSURL: server.URL + "/.well-known/jwks.json", JWKSCAFile: caFile,
		JWKSCacheTTL: "30s", JWKSStaleTTL: "60s", JWKSRefreshTimeout: "2s", JWKSMaxKeys: 8,
		Issuer: server.URL, Audience: "numou-iket",
	}
	gateway := &Gateway{config: &config.Config{}, logger: logging.NewLogger(false)}
	middleware := gateway.jwtAuthMiddleware(cfg)

	claims := validNumouClaims(time.Now())
	claims["iss"] = server.URL
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "active-2026-11"
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign Numou access token: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	response := httptest.NewRecorder()
	middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected JWKS-verified token to pass, got %d: %s", response.Code, response.Body.String())
	}
}

func TestNumouJWTMiddlewareReturns503WhenJWKSIsUnavailable(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "identity unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	dir := t.TempDir()
	caFile := filepath.Join(dir, "identity-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caFile, caPEM, 0600); err != nil {
		t.Fatalf("write JWKS CA: %v", err)
	}
	cfg := config.JWTConfig{
		Enabled: true, NumouAccessTokens: true, Algorithms: []string{"RS256"},
		JWKSURL: server.URL + "/.well-known/jwks.json", JWKSCAFile: caFile,
		JWKSCacheTTL: "30s", JWKSStaleTTL: "60s", JWKSRefreshTimeout: "2s", JWKSMaxKeys: 8,
		Issuer: server.URL, Audience: "numou-iket",
	}
	gateway := &Gateway{config: &config.Config{}, logger: logging.NewLogger(false)}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	claims := validNumouClaims(time.Now())
	claims["iss"] = server.URL
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "unavailable"
	raw, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign Numou access token: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	response := httptest.NewRecorder()
	gateway.jwtAuthMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected JWKS outage to fail closed with 503, got %d: %s", response.Code, response.Body.String())
	}
}
