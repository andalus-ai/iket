package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bhangun/iket/pkg/core/authcontext"
)

func TestNumouMachineRouteScopesRequireEveryConfiguredScope(t *testing.T) {
	called := false
	handler := requireScopesMiddleware([]string{"orders.read", "orders.write"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name string
		scopes []string
		wantStatus int
		wantCalled bool
	}{
		{name: "all scopes granted", scopes: []string{"orders.read", "orders.write"}, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "one required scope missing", scopes: []string{"orders.read"}, wantStatus: http.StatusForbidden},
		{name: "unrelated scope only", scopes: []string{"profile.read"}, wantStatus: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			ctx := authcontext.WithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil).Context(), authcontext.Principal{
				Source: "jwt", ClientID: "nmc_client_test", TokenUse: "access", Scopes: tc.scopes,
			})
			req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus { t.Fatalf("status=%d want=%d body=%q", rec.Code, tc.wantStatus, rec.Body.String()) }
			if called != tc.wantCalled { t.Fatalf("handler called=%v want=%v", called, tc.wantCalled) }
		})
	}
}

func TestLegacyRouteScopeAuthorizationRetainsAnyOfSemantics(t *testing.T) {
	handler := requireScopesMiddleware([]string{"orders.read", "orders.write"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx := authcontext.WithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil).Context(), authcontext.Principal{
		Source: "jwt", Scopes: []string{"orders.read"},
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("legacy any-of behavior changed: status=%d body=%q", rec.Code, rec.Body.String())
	}
}
