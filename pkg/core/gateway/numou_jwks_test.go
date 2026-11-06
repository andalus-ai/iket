package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bhangun/iket/pkg/config"
)

func TestNumouJWKSResolverRotatesSigningKeysByKID(t *testing.T) {
	oldKey := mustGenerateJWKSKey(t)
	newKey := mustGenerateJWKSKey(t)
	var document atomic.Value
	document.Store(mustJWKSJSON(t, jwkForTest("old-key", &oldKey.PublicKey)))
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(document.Load().([]byte))
	}))
	defer server.Close()

	resolver := newTestJWKSResolver(t, server)
	gotOld, err := resolver.key(context.Background(), "old-key")
	if err != nil || gotOld.N.Cmp(oldKey.N) != 0 {
		t.Fatalf("expected old key, got key=%v err=%v", gotOld, err)
	}

	// Publishing a new kid causes an early refresh even while the previous
	// key set is within its normal cache TTL.
	document.Store(mustJWKSJSON(t, jwkForTest("new-key", &newKey.PublicKey)))
	gotNew, err := resolver.key(context.Background(), "new-key")
	if err != nil || gotNew.N.Cmp(newKey.N) != 0 {
		t.Fatalf("expected rotated key, got key=%v err=%v", gotNew, err)
	}
	if _, err := resolver.key(context.Background(), "old-key"); !errors.Is(err, errJWKSUnknownKey) {
		t.Fatalf("expected removed old key to be rejected after refresh, got %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected initial load and one rotation refresh, got %d requests", requests.Load())
	}
}

func TestNumouJWKSResolverUsesStaleKnownKeyOnlyWithinBound(t *testing.T) {
	key := mustGenerateJWKSKey(t)
	var unavailable atomic.Bool
	body := mustJWKSJSON(t, jwkForTest("active", &key.PublicKey))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	resolver := newTestJWKSResolver(t, server)
	if _, err := resolver.key(context.Background(), "active"); err != nil {
		t.Fatalf("initial JWKS load failed: %v", err)
	}
	base := time.Now()
	resolver.now = func() time.Time { return base }
	resolver.mu.Lock()
	resolver.expiresAt = base.Add(-time.Second)
	resolver.mu.Unlock()
	unavailable.Store(true)

	base = base.Add(31 * time.Second)
	if got, err := resolver.key(context.Background(), "active"); err != nil || got.N.Cmp(key.N) != 0 {
		t.Fatalf("expected stale known key during bounded outage, got key=%v err=%v", got, err)
	}

	base = base.Add(31 * time.Second)
	if _, err := resolver.key(context.Background(), "active"); !errors.Is(err, errJWKSUnavailable) {
		t.Fatalf("expected stale key to expire and fail closed, got %v", err)
	}
}

func TestNumouJWKSResolverBoundsUnknownKIDRefreshes(t *testing.T) {
	key := mustGenerateJWKSKey(t)
	var requests atomic.Int32
	body := mustJWKSJSON(t, jwkForTest("known", &key.PublicKey))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	resolver := newTestJWKSResolver(t, server)
	if _, err := resolver.key(context.Background(), "known"); err != nil {
		t.Fatalf("initial JWKS load failed: %v", err)
	}
	for n := 0; n < 20; n++ {
		if _, err := resolver.key(context.Background(), fmt.Sprintf("attacker-kid-%d", n)); !errors.Is(err, errJWKSUnknownKey) {
			t.Fatalf("expected unknown kid rejection, got %v", err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("unknown kid flood caused %d JWKS requests; expected one", requests.Load())
	}
}

func TestNumouJWKSResolverRejectsMalformedAndOversizedKeySets(t *testing.T) {
	key := mustGenerateJWKSKey(t)
	cases := []struct {
		name string
		body []byte
	}{
		{name: "malformed", body: []byte(`{"keys":[` )},
		{name: "weak modulus", body: mustJWKSJSON(t, jwk{KeyType: "RSA", Use: "sig", Alg: "RS256", KeyID: "weak", Modulus: base64.RawURLEncoding.EncodeToString([]byte{1}), Exponent: "AQAB"})},
		{name: "duplicate kid", body: mustJWKSJSON(t, jwkForTest("dup", &key.PublicKey), jwkForTest("dup", &key.PublicKey))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(tc.body)
			}))
			defer server.Close()
			resolver := newTestJWKSResolver(t, server)
			if _, err := resolver.key(context.Background(), "whatever"); !errors.Is(err, errJWKSUnavailable) {
				t.Fatalf("expected malformed JWKS to fail closed, got %v", err)
			}
		})
	}
}

func newTestJWKSResolver(t *testing.T, server *httptest.Server) *numouJWKSResolver {
	t.Helper()
	resolver, err := newNumouJWKSResolverWithClient(config.JWTConfig{
		JWKSURL: server.URL, JWKSCacheTTL: "30s", JWKSStaleTTL: "60s",
		JWKSMaxKeys: 8,
	}, server.Client())
	if err != nil {
		t.Fatalf("create JWKS resolver: %v", err)
	}
	return resolver
}

func mustGenerateJWKSKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func jwkForTest(kid string, key *rsa.PublicKey) jwk {
	exponent := big.NewInt(int64(key.E)).Bytes()
	return jwk{
		KeyType: "RSA", Use: "sig", Alg: "RS256", KeyID: kid,
		Modulus: base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		Exponent: base64.RawURLEncoding.EncodeToString(exponent),
	}
}

func mustJWKSJSON(t *testing.T, keys ...jwk) []byte {
	t.Helper()
	body, err := json.Marshal(jwksDocument{Keys: keys})
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}
	return body
}
