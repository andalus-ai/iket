package gateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bhangun/iket/pkg/config"
	"github.com/golang-jwt/jwt/v4"
)

// This exercises the actual configured HTTP transport and both Identity
// endpoints. The in-process Identity fixture requires a verified client
// certificate, issues a workload token, then checks that token separately
// from the application access token being introspected.
func TestNumouIdentityMTLSClientCredentialsAndIntrospectionEndToEnd(t *testing.T) {
	clientCA, clientCAKey, clientCertPEM, clientKeyPEM := testClientCertificate(t)
	clientCAPool := x509.NewCertPool()
	clientCAPool.AddCert(clientCA)

	var tokenCalls, introspectionCalls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/oauth/token":
			tokenCalls.Add(1)
			user, pass, ok := r.BasicAuth()
			if !ok || user != "iket-workload" || pass != "mounted-secret" {
				http.Error(w, "invalid workload client", http.StatusUnauthorized)
				return
			}
			if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("scope") != "oauth-introspect" {
				http.Error(w, "invalid grant", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(workloadTokenResponse{
				AccessToken: "identity-workload-token", TokenType: "Bearer",
				ExpiresIn: 300, Scope: "oauth-introspect",
			})
		case "/internal/v1/oauth2/introspect":
			introspectionCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer identity-workload-token" {
				http.Error(w, "workload role token required", http.StatusForbidden)
				return
			}
			if err := r.ParseForm(); err != nil || r.Form.Get("token") != "application-access-token" {
				http.Error(w, "application token missing", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(numouIntrospectionResponse{
				Active: true, TokenType: "Bearer", ClientID: "nmc_client_test",
				TenantID: "tenant-123", ApplicationID: "application-456",
				Scope: "orders.read orders.write",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	server.TLS = &tls.Config{ClientCAs: clientCAPool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()

	// Trust the test server's exact certificate; production uses the Identity CA.
	serverCert := server.Certificate()
	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Raw})
	dir := t.TempDir()
	caFile := filepath.Join(dir, "identity-ca.pem")
	certFile := filepath.Join(dir, "iket-client.pem")
	keyFile := filepath.Join(dir, "iket-client-key.pem")
	secretFile := filepath.Join(dir, "workload-secret")
	for path, data := range map[string][]byte{
		caFile: serverCertPEM, certFile: clientCertPEM,
		keyFile: clientKeyPEM, secretFile: []byte("mounted-secret\n"),
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatalf("write test credential %s: %v", path, err)
		}
	}

	cfg := config.NumouIntrospectionConfig{
		Enabled: true, URL: server.URL + "/internal/v1/oauth2/introspect",
		TokenURL: server.URL + "/oauth/token", ClientID: "iket-workload",
		ClientSecretFile: secretFile, WorkloadScope: "oauth-introspect",
		CAFile: caFile, ClientCertFile: certFile, ClientKeyFile: keyFile,
		Timeout: "2s", PositiveCacheTTL: "5s", NegativeCacheTTL: "1s",
		CacheMaxEntries: 32,
	}
	introspector, err := newNumouIntrospector(cfg)
	if err != nil { t.Fatalf("initialize configured mTLS introspector: %v", err) }

	claims := jwt.MapClaims{
		"client_id": "nmc_client_test", "tenant_id": "tenant-123",
		"application_id": "application-456", "scope": "orders.read orders.write",
		"exp": float64(time.Now().Add(time.Minute).Unix()),
	}
	if err := introspector.validate(context.Background(), "application-access-token", claims); err != nil {
		t.Fatalf("expected complete Identity exchange to validate token: %v", err)
	}
	if tokenCalls.Load() != 1 || introspectionCalls.Load() != 1 {
		t.Fatalf("expected one workload exchange and one introspection, got token=%d introspection=%d", tokenCalls.Load(), introspectionCalls.Load())
	}
	// A repeated request should reuse both the workload token and the positive
	// introspection result without another network round trip.
	if err := introspector.validate(context.Background(), "application-access-token", claims); err != nil {
		t.Fatalf("expected cached token validation: %v", err)
	}
	if tokenCalls.Load() != 1 || introspectionCalls.Load() != 1 {
		t.Fatalf("expected cached validation, got token=%d introspection=%d", tokenCalls.Load(), introspectionCalls.Load())
	}
	_ = clientCAKey
}

func testClientCertificate(t *testing.T) (*x509.Certificate, *rsa.PrivateKey, []byte, []byte) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil { t.Fatal(err) }
	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1001), Subject: pkix.Name{CommonName: "Iket Test Client CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil { t.Fatal(err) }
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil { t.Fatal(err) }
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil { t.Fatal(err) }
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1002), Subject: pkix.Name{CommonName: "iket-workload"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	if err != nil { t.Fatal(err) }
	clientKeyDER := x509.MarshalPKCS1PrivateKey(clientKey)
	return caCert, caKey,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: clientKeyDER})
}

// Keep the helper's PEM output strict and make accidental empty material fail
// close before the integration fixture is started.
func TestNumouTestClientCertificateMaterialIsPEM(t *testing.T) {
	_, _, certPEM, keyPEM := testClientCertificate(t)
	if !strings.Contains(string(certPEM), "BEGIN CERTIFICATE") || !strings.Contains(string(keyPEM), "BEGIN RSA PRIVATE KEY") {
		t.Fatal("test client certificate material is not PEM encoded")
	}
}
