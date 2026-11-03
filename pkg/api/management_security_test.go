package api

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bhangun/iket/pkg/config"
)

func TestManagementSecurityMiddleware(t *testing.T) {
	security := config.SecurityConfig{
		EnableBasicAuth: true,
		BasicAuthUsers: map[string]string{"operator": "correct-horse"},
	}
	api := &ManagementAPI{}
	api.SetSecurityConfig(security)

	nextCalled := false
	handler := api.managementSecurityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("rejects plaintext HTTP even with valid Basic Auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/status", nil)
		req.SetBasicAuth("operator", "correct-horse")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", rec.Code)
		}
		if nextCalled {
			t.Fatal("handler must not run for plaintext management request")
		}
	})

	t.Run("rejects missing or invalid Basic Auth after mTLS", func(t *testing.T) {
		for _, credentials := range [][2]string{{"", ""}, {"operator", "wrong"}} {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/status", nil)
			req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
			if credentials[0] != "" {
				req.SetBasicAuth(credentials[0], credentials[1])
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rec.Code)
			}
		}
		if nextCalled {
			t.Fatal("handler must not run for invalid credentials")
		}
	})

	t.Run("accepts verified mTLS and configured operator credentials", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/status", nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
		req.SetBasicAuth("operator", "correct-horse")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
		if !nextCalled {
			t.Fatal("expected authenticated request to reach handler")
		}
	})
}

func TestManagementSecurityMiddlewareFailsClosedWithoutConfiguration(t *testing.T) {
	api := &ManagementAPI{}
	handler := api.managementSecurityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unconfigured management handler must not execute")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}
