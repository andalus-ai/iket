package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bhangun/iket/pkg/config"
	gatewaypkg "github.com/bhangun/iket/pkg/core/gateway"
	"github.com/bhangun/iket/pkg/logging"
)

func TestConfigETagIsStableAndChangesWithConfig(t *testing.T) {
	first := &config.Config{Server: config.ServerConfig{Port: 8080}}
	second := &config.Config{Server: config.ServerConfig{Port: 9090}}
	a, err := configETag(first)
	if err != nil { t.Fatal(err) }
	again, err := configETag(first)
	if err != nil { t.Fatal(err) }
	b, err := configETag(second)
	if err != nil { t.Fatal(err) }
	if a != again { t.Fatalf("expected stable ETag, got %q and %q", a, again) }
	if a == b { t.Fatalf("expected changed config to produce a different ETag") }
}

func TestIfMatchConfigETag(t *testing.T) {
	const current = `"sha256:abc"`
	for _, tc := range []struct{ header string; want bool }{
		{current, true},
		{`"sha256:old", "sha256:abc"`, true},
		{"*", true},
		{`W/"sha256:abc"`, false},
		{`"sha256:old"`, false},
	} {
		if got := ifMatchConfigETag(tc.header, current); got != tc.want {
			t.Errorf("ifMatchConfigETag(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestGatewayConfigETagAndStaleIfMatchConflict(t *testing.T) {
	cfg := &config.Config{Server: config.ServerConfig{Port: 8080}}
	gw, err := gatewaypkg.NewGateway(gatewaypkg.Dependencies{Config: cfg, Logger: logging.NewLogger(false)}, "test")
	if err != nil { t.Fatal(err) }
	api := NewManagementAPI(gw, logging.NewLogger(false), nil)

	get := httptest.NewRecorder()
	api.getGatewayConfig(get, httptest.NewRequest(http.MethodGet, "/api/v1/gateway/config", nil))
	if get.Code != http.StatusOK { t.Fatalf("GET status = %d: %s", get.Code, get.Body.String()) }
	etag := get.Header().Get("ETag")
	if etag == "" { t.Fatal("GET did not return ETag") }

	body := strings.NewReader(`{"server":{"port":9090}}`)
	stale := httptest.NewRequest(http.MethodPut, "/api/v1/gateway/config", body)
	stale.Header.Set("If-Match", `"sha256:stale"`)
	response := httptest.NewRecorder()
	api.updateGatewayConfig(response, stale)
	if response.Code != http.StatusConflict { t.Fatalf("stale update status = %d, want 409: %s", response.Code, response.Body.String()) }
	if response.Header().Get("ETag") != etag { t.Fatalf("conflict should return current ETag") }
	if got := gw.GetConfig().Server.Port; got != 8080 { t.Fatalf("stale update changed live config port to %d", got) }
}
