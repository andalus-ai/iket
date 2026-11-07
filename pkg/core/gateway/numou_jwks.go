package gateway

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/config"
)

const maxJWKSResponseBytes = 64 * 1024
const minJWKSRefreshInterval = 5 * time.Second

var (
	errJWKSUnavailable = errors.New("Numou JWKS is temporarily unavailable")
	errJWKSUnknownKey  = errors.New("Numou JWKS does not contain the requested key")
)

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	KeyType  string `json:"kty"`
	Use      string `json:"use,omitempty"`
	Alg      string `json:"alg,omitempty"`
	KeyID    string `json:"kid"`
	Modulus  string `json:"n"`
	Exponent string `json:"e"`
	KeyOps   []string `json:"key_ops,omitempty"`
	D        string   `json:"d,omitempty"`
	P        string   `json:"p,omitempty"`
	Q        string   `json:"q,omitempty"`
	DP       string   `json:"dp,omitempty"`
	DQ       string   `json:"dq,omitempty"`
	QI       string   `json:"qi,omitempty"`
	K        string   `json:"k,omitempty"`
	Oth      []json.RawMessage `json:"oth,omitempty"`
}

type numouJWKSResolver struct {
	client      *http.Client
	endpoint    string
	cacheTTL    time.Duration
	staleTTL    time.Duration
	maxKeys     int
	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	expiresAt   time.Time
	staleUntil  time.Time
	lastAttempt time.Time
	lastFailure bool
	now         func() time.Time
}

func newNumouJWKSResolver(cfg config.JWTConfig) (*numouJWKSResolver, error) {
	timeout, err := time.ParseDuration(cfg.JWKSRefreshTimeout)
	if err != nil || timeout <= 0 || timeout > 10*time.Second {
		return nil, errors.New("invalid Numou JWKS refresh timeout")
	}
	issuer, err := url.Parse(strings.TrimSpace(cfg.Issuer))
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" {
		return nil, errors.New("Numou issuer must be HTTPS when JWKS is enabled")
	}
	endpoint, err := url.Parse(strings.TrimSpace(cfg.JWKSURL))
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" || !strings.EqualFold(endpoint.Host, issuer.Host) {
		return nil, errors.New("Numou JWKS endpoint must be HTTPS on the configured Identity issuer host")
	}
	var roots *x509.CertPool
	if strings.TrimSpace(cfg.JWKSCAFile) != "" {
		caPEM, readErr := os.ReadFile(cfg.JWKSCAFile)
		if readErr != nil {
			return nil, errors.New("unable to read Numou JWKS CA file")
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("Numou JWKS CA file contains no certificates")
		}
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		Proxy: http.ProxyFromEnvironment,
		MaxIdleConns: 16,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout: 90 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	return newNumouJWKSResolverWithClient(cfg, client)
}

func newNumouJWKSResolverWithClient(cfg config.JWTConfig, client *http.Client) (*numouJWKSResolver, error) {
	cacheTTL, err := time.ParseDuration(cfg.JWKSCacheTTL)
	if err != nil || cacheTTL < 30*time.Second || cacheTTL > time.Hour {
		return nil, errors.New("invalid Numou JWKS cache TTL")
	}
	staleTTL, err := time.ParseDuration(cfg.JWKSStaleTTL)
	if err != nil || staleTTL < cacheTTL || staleTTL > time.Hour {
		return nil, errors.New("invalid Numou JWKS stale TTL")
	}
	if cfg.JWKSMaxKeys < 1 || cfg.JWKSMaxKeys > 32 {
		return nil, errors.New("invalid Numou JWKS maximum key count")
	}
	endpoint, err := url.Parse(strings.TrimSpace(cfg.JWKSURL))
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, errors.New("Numou JWKS endpoint must be a valid HTTPS URL")
	}
	if client == nil {
		return nil, errors.New("Numou JWKS HTTP client is required")
	}
	cloned := *client
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &numouJWKSResolver{
		client: &cloned, endpoint: cfg.JWKSURL, cacheTTL: cacheTTL,
		staleTTL: staleTTL, maxKeys: cfg.JWKSMaxKeys,
		keys: make(map[string]*rsa.PublicKey), now: time.Now,
	}, nil
}

// key serializes refreshes. Unknown kids may trigger an early refresh, but the
// minimum refresh interval bounds attacker-controlled cache-miss traffic.
func (r *numouJWKSResolver) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if kid == "" || len(kid) > 128 {
		return nil, errJWKSUnknownKey
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	cached := r.keys[kid]
	fresh := len(r.keys) > 0 && now.Before(r.expiresAt)
	if cached != nil && fresh {
		return cached, nil
	}

	shouldRefresh := !fresh
	if cached == nil {
		shouldRefresh = r.lastAttempt.IsZero() || now.Sub(r.lastAttempt) >= minJWKSRefreshInterval
	} else if shouldRefresh && !r.lastAttempt.IsZero() && now.Sub(r.lastAttempt) < minJWKSRefreshInterval {
		if now.Before(r.staleUntil) {
			return cached, nil
		}
		return nil, errJWKSUnavailable
	}
	if shouldRefresh {
		r.lastAttempt = now
		if err := r.refreshLocked(ctx); err != nil {
			r.lastFailure = true
			if cached != nil && r.now().Before(r.staleUntil) {
				return cached, nil
			}
			return nil, err
		}
		r.lastFailure = false
		if key := r.keys[kid]; key != nil {
			return key, nil
		}
		return nil, errJWKSUnknownKey
	}
	if cached != nil && now.Before(r.staleUntil) {
		return cached, nil
	}
	if cached != nil || r.lastFailure {
		return nil, errJWKSUnavailable
	}
	return nil, errJWKSUnknownKey
}

func (r *numouJWKSResolver) refreshLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint, nil)
	if err != nil {
		return errJWKSUnavailable
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return errJWKSUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errJWKSUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSResponseBytes+1))
	if err != nil || len(body) > maxJWKSResponseBytes {
		return errJWKSUnavailable
	}
	var doc jwksDocument
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&doc); err != nil {
		return errJWKSUnavailable
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errJWKSUnavailable
	}
	if len(doc.Keys) == 0 || len(doc.Keys) > r.maxKeys {
		return errJWKSUnavailable
	}
	next := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, item := range doc.Keys {
		if item.D != "" || item.P != "" || item.Q != "" || item.DP != "" ||
			item.DQ != "" || item.QI != "" || item.K != "" || len(item.Oth) > 0 {
			return errJWKSUnavailable
		}
		if item.Use != "" && item.Use != "sig" {
			continue
		}
		if item.Alg != "" && item.Alg != "RS256" {
			continue
		}
		if item.KeyOps != nil && !contains(item.KeyOps, "verify") {
			continue
		}
		if item.KeyType != "RSA" {
			continue
		}
		if item.KeyID == "" || len(item.KeyID) > 128 {
			return errJWKSUnavailable
		}
		if _, exists := next[item.KeyID]; exists {
			return errJWKSUnavailable
		}
		modulus, decodeErr := base64.RawURLEncoding.DecodeString(item.Modulus)
		if decodeErr != nil || len(modulus) < 256 || len(modulus) > 1024 {
			return errJWKSUnavailable
		}
		exponentBytes, decodeErr := base64.RawURLEncoding.DecodeString(item.Exponent)
		if decodeErr != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			return errJWKSUnavailable
		}
		exponent := 0
		for _, b := range exponentBytes {
			exponent = (exponent << 8) | int(b)
		}
		if exponent < 3 || exponent%2 == 0 {
			return errJWKSUnavailable
		}
		n := new(big.Int).SetBytes(modulus)
		if n.BitLen() < 2048 || n.BitLen() > 8192 {
			return errJWKSUnavailable
		}
		next[item.KeyID] = &rsa.PublicKey{N: n, E: exponent}
	}
	if len(next) == 0 {
		return errJWKSUnavailable
	}
	now := r.now()
	r.keys = next
	r.expiresAt = now.Add(r.cacheTTL)
	r.staleUntil = now.Add(r.staleTTL)
	return nil
}

