package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

func testIntrospector(server *httptest.Server, positive, negative time.Duration) *numouIntrospector {
	return &numouIntrospector{
		client: server.Client(), endpoint: server.URL,
		positiveTTL: positive, negativeTTL: negative, maxEntries: 16,
		cache: make(map[string]introspectionCacheEntry),
		flights: make(map[string]*introspectionFlight), now: time.Now,
		getWorkloadToken: func(context.Context) (string, error) { return "test-workload-bearer", nil },
	}
}

func validIntrospectionClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"client_id": "nmc_client_test",
		"tenant_id": "8d5d8a7e-6d6b-4f0a-9d1f-7cfa8f35c2b1",
		"application_id": "b2b1b84d-23d8-4a9b-8b42-4f2e7f2d6c11",
		"scope": "orders.read orders.write",
		"exp": float64(time.Now().Add(time.Minute).Unix()),
	}
}

func TestNumouIntrospectionAcceptsActiveAndCachesByTokenDigest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-workload-bearer" { t.Errorf("workload bearer token missing") }
		if err := r.ParseForm(); err != nil { t.Errorf("parse form: %v", err) }
		if r.Form.Get("token") != "sensitive-token-value" { t.Errorf("submitted access token missing") }
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"active": true, "token_type": "Bearer", "client_id": "nmc_client_test",
			"tenant_id": "8d5d8a7e-6d6b-4f0a-9d1f-7cfa8f35c2b1",
			"application_id": "b2b1b84d-23d8-4a9b-8b42-4f2e7f2d6c11",
			"scope": "orders.read orders.write",
		})
	}))
	defer server.Close()

	i := testIntrospector(server, 5*time.Second, time.Second)
	claims := validIntrospectionClaims()
	for n := 0; n < 2; n++ {
		if err := i.validate(context.Background(), "sensitive-token-value", claims); err != nil {
			t.Fatalf("expected active token, got %v", err)
		}
	}
	if got := calls.Load(); got != 1 { t.Fatalf("expected cached second request, got %d calls", got) }
}

func TestNumouIntrospectionRejectsInactiveAndCachesBriefly(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"active":false}`))
	}))
	defer server.Close()

	i := testIntrospector(server, 5*time.Second, time.Second)
	for n := 0; n < 2; n++ {
		if err := i.validate(context.Background(), "inactive-token", validIntrospectionClaims()); !errors.Is(err, errNumouTokenInactive) {
			t.Fatalf("expected inactive sentinel, got %v", err)
		}
	}
	if got := calls.Load(); got != 1 { t.Fatalf("expected negative cache, got %d calls", got) }
}

func TestNumouIntrospectionFailsClosedOnIdentityMismatchAndMalformedResponse(t *testing.T) {
	tests := []struct{name, body string}{
		{"tenant mismatch", `{"active":true,"token_type":"Bearer","client_id":"nmc_client_test","tenant_id":"different","application_id":"b2b1b84d-23d8-4a9b-8b42-4f2e7f2d6c11","scope":"orders.read orders.write"}`},
		{"scope escalation", `{"active":true,"token_type":"Bearer","client_id":"nmc_client_test","tenant_id":"8d5d8a7e-6d6b-4f0a-9d1f-7cfa8f35c2b1","application_id":"b2b1b84d-23d8-4a9b-8b42-4f2e7f2d6c11","scope":"orders.read admin"}`},
		{"unknown response field", `{"active":true,"token_type":"Bearer","client_id":"nmc_client_test","tenant_id":"8d5d8a7e-6d6b-4f0a-9d1f-7cfa8f35c2b1","application_id":"b2b1b84d-23d8-4a9b-8b42-4f2e7f2d6c11","scope":"orders.read orders.write","secret":"leak"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			i := testIntrospector(server, 5*time.Second, time.Second)
			if err := i.validate(context.Background(), "token-"+tc.name, validIntrospectionClaims()); err == nil {
				t.Fatal("expected fail-closed validation error")
			}
		})
	}
}

func TestNumouIntrospectionDoesNotFollowRedirectsOrAcceptNon200(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/steal", http.StatusFound)
	}))
	defer redirect.Close()
	i := testIntrospector(redirect, 5*time.Second, time.Second)
	if err := i.validate(context.Background(), "redirect-token", validIntrospectionClaims()); err == nil {
		t.Fatal("expected non-200 response to fail closed")
	}
}

func TestNumouIntrospectionRequestUsesFormEncoding(t *testing.T) {
	form := url.Values{"token": []string{"token+with&reserved"}}
	if got := form.Encode(); got != "token=token%2Bwith%26reserved" {
		t.Fatalf("unexpected encoded token form: %s", got)
	}
}

func TestAcquireWorkloadTokenUsesClientCredentialsAndCachesShortLivedToken(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/token" { t.Errorf("path=%s want /token", r.URL.Path) }
		user, pass, ok := r.BasicAuth()
		if !ok || user != "nmc_iket_introspection" || pass != "mounted-secret" {
			t.Errorf("unexpected workload client authentication")
		}
		if err := r.ParseForm(); err != nil { t.Errorf("parse form: %v", err) }
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("scope") != "oauth-introspect" {
			t.Errorf("unexpected grant form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"workload-access-token","token_type":"Bearer","expires_in":300,"scope":"oauth-introspect"}`))
	}))
	defer server.Close()
	secretFile := t.TempDir() + "/client-secret"
	if err := os.WriteFile(secretFile, []byte("mounted-secret\n"), 0600); err != nil { t.Fatal(err) }
	i := &numouIntrospector{
		client: server.Client(), tokenEndpoint: server.URL + "/token",
		clientID: "nmc_iket_introspection", clientSecretFile: secretFile,
		workloadScope: "oauth-introspect", now: time.Now,
	}
	first, err := i.acquireWorkloadToken(context.Background())
	if err != nil { t.Fatal(err) }
	second, err := i.acquireWorkloadToken(context.Background())
	if err != nil { t.Fatal(err) }
	if first != "workload-access-token" || second != first { t.Fatalf("unexpected cached tokens %q / %q", first, second) }
	if got := calls.Load(); got != 1 { t.Fatalf("expected one token exchange, got %d", got) }
}

func TestAcquireWorkloadTokenRejectsMissingRoleScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"token","token_type":"Bearer","expires_in":300,"scope":"orders.read"}`))
	}))
	defer server.Close()
	secretFile := t.TempDir() + "/client-secret"
	if err := os.WriteFile(secretFile, []byte("secret"), 0600); err != nil { t.Fatal(err) }
	i := &numouIntrospector{client: server.Client(), tokenEndpoint: server.URL, clientID: "client",
		clientSecretFile: secretFile, workloadScope: "oauth-introspect", now: time.Now}
	if _, err := i.acquireWorkloadToken(context.Background()); err == nil {
		t.Fatal("expected token without oauth-introspect scope to be rejected")
	}
}
