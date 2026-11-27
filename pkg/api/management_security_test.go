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

func TestManagementSecurityMiddlewareFailsClosedWithEmptyBootstrapPassword(t *testing.T) {
	api := &ManagementAPI{}
	api.SetSecurityConfig(config.SecurityConfig{
		EnableBasicAuth: true,
		BasicAuthUsers: map[string]string{"admin": ""},
	})
	handler := api.managementSecurityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("empty bootstrap password must never authenticate")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateway/status", nil)
	req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
	req.SetBasicAuth("admin", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for empty bootstrap password, got %d", rec.Code)
	}
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


func TestManagementRBAC(t *testing.T) {
	tests := []struct {
		name       string
		username   string
		role       string
		method     string
		path       string
		wantStatus int
	}{
		{"viewer can read status", "read", "viewer", http.MethodGet, "/api/v1/gateway/status", http.StatusNoContent},
		{"viewer cannot mutate routes", "read", "viewer", http.MethodPost, "/api/v1/routes", http.StatusForbidden},
		{"viewer cannot read sensitive gateway config", "read", "viewer", http.MethodGet, "/api/v1/gateway/config", http.StatusForbidden},
		{"operator can mutate routes", "ops", "operator", http.MethodPost, "/api/v1/routes", http.StatusNoContent},
		{"operator cannot change gateway config", "ops", "operator", http.MethodPut, "/api/v1/gateway/config", http.StatusForbidden},
		{"operator cannot manage certificates", "ops", "operator", http.MethodDelete, "/api/v1/certificates/cert-1", http.StatusForbidden},
		{"admin can manage certificates", "root", "admin", http.MethodDelete, "/api/v1/certificates/cert-1", http.StatusNoContent},
		{"unknown role is denied", "unknown", "owner", http.MethodGet, "/api/v1/gateway/status", http.StatusForbidden},
		{"unknown method is denied", "ops", "operator", http.MethodConnect, "/api/v1/routes", http.StatusForbidden},
	}

	users := map[string]string{"read": "read-secret", "ops": "ops-secret", "root": "root-secret", "unknown": "unknown-secret"}
	roles := map[string]string{"read": "viewer", "ops": "operator", "root": "admin", "unknown": "owner"}
	api := &ManagementAPI{}
	api.SetSecurityConfig(config.SecurityConfig{
		EnableBasicAuth: true,
		BasicAuthUsers: users,
		BasicAuthRoles: roles,
	})
	// Verify SetSecurityConfig retained an immutable snapshot.
	roles["read"] = "admin"
	users["read"] = "changed"

	handler := api.managementSecurityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
			req.SetBasicAuth(tt.username, map[string]string{
				"read": "read-secret", "ops": "ops-secret", "root": "root-secret", "unknown": "unknown-secret",
			}[tt.username])
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("expected %d, got %d", tt.wantStatus, rec.Code)
			}
		})
	}
}

func TestManagementRequiredRole(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodGet, "/api/v1/logs/stream", managementRoleViewer},
		{http.MethodGet, "/api/v1/ws/status", managementRoleViewer},
		{http.MethodPost, "/api/v1/routes", managementRoleOperator},
		{http.MethodPut, "/api/v1/services/example", managementRoleOperator},
		{http.MethodGet, "/api/v1/clients", managementRoleAdmin},
		{http.MethodGet, "/api/v1/certificates", managementRoleAdmin},
		{http.MethodPost, "/api/v1/backup", managementRoleAdmin},
		{http.MethodGet, "/api/v1/gateway/config", managementRoleAdmin},
		{http.MethodOptions, "/api/v1/routes", ""},
	}
	for _, tt := range tests {
		if got := managementRequiredRole(tt.method, tt.path); got != tt.want {
			t.Errorf("%s %s: expected role %q, got %q", tt.method, tt.path, tt.want, got)
		}
	}
}
