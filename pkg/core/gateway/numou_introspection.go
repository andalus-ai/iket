package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/config"
	"github.com/golang-jwt/jwt/v4"
)

const maxIntrospectionResponseBytes = 16 * 1024

var errNumouTokenInactive = errors.New("Numou access token is inactive")

type numouIntrospectionResponse struct {
	Active            bool   `json:"active"`
	TokenType         string `json:"token_type,omitempty"`
	ClientID          string `json:"client_id"`
	TenantID          string `json:"tenant_id"`
	ApplicationID     string `json:"application_id"`
	Scope             string `json:"scope"`
}

type introspectionCacheEntry struct {
	active   bool
	expires  time.Time
}

type introspectionFlight struct {
	done chan struct{}
	err  error
}

type numouIntrospector struct {
	client           *http.Client
	endpoint         string
	tokenEndpoint    string
	clientID         string
	clientSecretFile string
	workloadScope    string
	tokenMu          sync.Mutex
	workloadToken    string
	workloadTokenExp time.Time
	getWorkloadToken func(context.Context) (string, error)
	positiveTTL      time.Duration
	negativeTTL      time.Duration
	maxEntries       int
	mu               sync.Mutex
	cache            map[string]introspectionCacheEntry
	flights          map[string]*introspectionFlight
	now              func() time.Time
}

func newNumouIntrospector(cfg config.NumouIntrospectionConfig) (*numouIntrospector, error) {
	rootsPEM, err := os.ReadFile(cfg.CAFile)
	if err != nil { return nil, err }
	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM(rootsPEM); !ok { return nil, errors.New("introspection CA file contains no certificates") }
	cert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
	if err != nil { return nil, err }
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil || timeout <= 0 || timeout > 10*time.Second { return nil, errors.New("invalid introspection timeout") }
	positive, err := time.ParseDuration(cfg.PositiveCacheTTL)
	if err != nil || positive <= 0 || positive > 5*time.Second { return nil, errors.New("invalid introspection positive cache TTL") }
	negative, err := time.ParseDuration(cfg.NegativeCacheTTL)
	if err != nil || negative <= 0 || negative > time.Second { return nil, errors.New("invalid introspection negative cache TTL") }
	if cfg.CacheMaxEntries < 1 || cfg.CacheMaxEntries > 100000 { return nil, errors.New("invalid introspection cache capacity") }
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil { return nil, errors.New("introspection endpoint must be HTTPS") }

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs: roots,
			Certificates: []tls.Certificate{cert},
		},
		Proxy: http.ProxyFromEnvironment,
		MaxIdleConns: 32,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout: 90 * time.Second,
	}
	i := &numouIntrospector{
		client: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		endpoint: cfg.URL, tokenEndpoint: cfg.TokenURL, clientID: cfg.ClientID,
		clientSecretFile: cfg.ClientSecretFile, workloadScope: cfg.WorkloadScope,
		positiveTTL: positive, negativeTTL: negative,
		maxEntries: cfg.CacheMaxEntries, cache: make(map[string]introspectionCacheEntry),
		flights: make(map[string]*introspectionFlight), now: time.Now,
	}
	i.getWorkloadToken = i.acquireWorkloadToken
	return i, nil
}

func (i *numouIntrospector) validate(ctx context.Context, rawToken string, claims jwt.MapClaims) error {
	digest := sha256.Sum256([]byte(rawToken))
	key := hex.EncodeToString(digest[:])
	now := i.now()

	i.mu.Lock()
	if entry, ok := i.cache[key]; ok {
		if now.Before(entry.expires) {
			i.mu.Unlock()
			if entry.active { return nil }
			return errNumouTokenInactive
		}
		delete(i.cache, key)
	}
	if flight, ok := i.flights[key]; ok {
		i.mu.Unlock()
		select {
		case <-ctx.Done(): return ctx.Err()
		case <-flight.done: return flight.err
		}
	}
	flight := &introspectionFlight{done: make(chan struct{})}
	i.flights[key] = flight
	i.mu.Unlock()

	active, err := i.request(ctx, rawToken, claims)
	i.mu.Lock()
	if err == nil {
		ttl := i.negativeTTL
		if active {
			ttl = i.positiveTTL
			if exp, ok := numericClaim(claims["exp"]); ok {
				remaining := time.Unix(int64(exp), 0).Sub(i.now())
				if remaining < ttl { ttl = remaining }
			}
		}
		if ttl > 0 {
			i.cache[key] = introspectionCacheEntry{active: active, expires: i.now().Add(ttl)}
			i.evictExpiredAndOverflowLocked()
		}
	}
	if !active && err == nil { err = errNumouTokenInactive }
	flight.err = err
	delete(i.flights, key)
	close(flight.done)
	i.mu.Unlock()
	return err
}

func (i *numouIntrospector) request(ctx context.Context, rawToken string, claims jwt.MapClaims) (bool, error) {
	form := url.Values{"token": []string{rawToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.endpoint, strings.NewReader(form.Encode()))
	if err != nil { return false, err }
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	workloadToken, err := i.getWorkloadToken(ctx)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+workloadToken)
	resp, err := i.client.Do(req)
	if err != nil { return false, err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return false, errors.New("Numou introspection returned non-success status") }
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIntrospectionResponseBytes+1))
	if err != nil { return false, err }
	if len(body) > maxIntrospectionResponseBytes { return false, errors.New("Numou introspection response too large") }
	var result numouIntrospectionResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil { return false, err }
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF { return false, errors.New("Numou introspection returned trailing JSON data") }
	if !result.Active { return false, nil }
	if result.TokenType != "Bearer" { return false, errors.New("Numou introspection returned an invalid token type") }
	if result.ClientID != stringClaim(claims, "client_id") ||
		result.TenantID != stringClaim(claims, "tenant_id") ||
		result.ApplicationID != stringClaim(claims, "application_id") {
		return false, errors.New("Numou introspection identity does not match verified token")
	}
	tokenScopes := strings.Fields(stringClaim(claims, "scope"))
	resultScopes := strings.Fields(result.Scope)
	if len(tokenScopes) == 0 || len(resultScopes) == 0 || !sameScopeSet(tokenScopes, resultScopes) {
		return false, errors.New("Numou introspection scopes do not match verified token")
	}
	return true, nil
}

func sameScopeSet(left, right []string) bool {
	if len(left) != len(right) { return false }
	a := append([]string(nil), left...)
	b := append([]string(nil), right...)
	sort.Strings(a); sort.Strings(b)
	for n := range a { if a[n] != b[n] { return false } }
	return true
}

func (i *numouIntrospector) evictExpiredAndOverflowLocked() {
	now := i.now()
	for key, entry := range i.cache { if !now.Before(entry.expires) { delete(i.cache, key) } }
	for len(i.cache) > i.maxEntries {
		var oldestKey string
		var oldest time.Time
		for key, entry := range i.cache {
			if oldestKey == "" || entry.expires.Before(oldest) { oldestKey, oldest = key, entry.expires }
		}
		delete(i.cache, oldestKey)
	}
}
