package api

import (
	"crypto/subtle"
	"net/http"

	"github.com/bhangun/iket/pkg/config"
)

// SetSecurityConfig installs the operator authentication policy used by every
// management REST, SSE, and WebSocket endpoint. A copy is retained so callers
// cannot mutate the policy after route registration.
func (api *ManagementAPI) SetSecurityConfig(security config.SecurityConfig) {
	security.BasicAuthUsers = cloneBasicAuthUsers(security.BasicAuthUsers)
	api.mu.Lock()
	defer api.mu.Unlock()
	api.security = &security
}

func cloneBasicAuthUsers(users map[string]string) map[string]string {
	if len(users) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(users))
	for username, password := range users {
		cloned[username] = password
	}
	return cloned
}

func (api *ManagementAPI) managementSecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.RLock()
		var security *config.SecurityConfig
		if api.security != nil {
			copy := *api.security
			security = &copy
		}
		api.mu.RUnlock()

		if security == nil || !security.EnableBasicAuth || len(security.BasicAuthUsers) == 0 {
			http.Error(w, "management authentication is not configured", http.StatusServiceUnavailable)
			return
		}
		// Management traffic must never send reusable Basic Auth credentials over
		// plaintext HTTP. VerifiedChains is populated only after successful client
		// certificate verification by the TLS server.
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
			http.Error(w, "mutual TLS client certificate required", http.StatusForbidden)
			return
		}
		username, password, ok := r.BasicAuth()
		expected, exists := security.BasicAuthUsers[username]
		if !ok || !exists || subtle.ConstantTimeCompare([]byte(password), []byte(expected)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Iket Management", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
