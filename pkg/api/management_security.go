package api

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/bhangun/iket/pkg/config"
	"go.uber.org/zap"
)

const (
	managementRoleViewer   = "viewer"
	managementRoleOperator = "operator"
	managementRoleAdmin    = "admin"
)

// SetSecurityConfig installs the operator authentication and authorization
// policy used by every management REST, SSE, and WebSocket endpoint.
func (api *ManagementAPI) SetSecurityConfig(security config.SecurityConfig) {
	security.BasicAuthUsers = cloneBasicAuthUsers(security.BasicAuthUsers)
	security.BasicAuthRoles = cloneBasicAuthRoles(security.BasicAuthRoles)
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

func cloneBasicAuthRoles(roles map[string]string) map[string]string {
	if len(roles) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(roles))
	for username, role := range roles {
		cloned[username] = strings.ToLower(strings.TrimSpace(role))
	}
	return cloned
}

func hasConfiguredBasicAuthUser(users map[string]string) bool {
	for username, password := range users {
		if username != "" && password != "" {
			return true
		}
	}
	return false
}

// managementRequiredRole returns the minimum role for a management request.
// Unknown methods are denied rather than treated as read-only.
func managementRequiredRole(method, path string) string {
	path = strings.TrimPrefix(path, "/api/v1")
	if isManagementAdminPath(path) {
		return managementRoleAdmin
	}
	switch method {
	case http.MethodGet, http.MethodHead:
		return managementRoleViewer
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return managementRoleOperator
	default:
		return ""
	}
}

func isManagementAdminPath(path string) bool {
	for _, prefix := range []string{
		"/gateway/config",
		"/certificates",
		"/enrollment",
		"/clients",
		"/backup",
		"/extensions",
		"/plugins", // plugin configuration and lifecycle can execute privileged code
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	if strings.HasSuffix(path, "/restore") {
		return true
	}
	return false
}

func managementRoleAllows(role, required string) bool {
	switch role {
	case managementRoleAdmin:
		return true
	case managementRoleOperator:
		return required == managementRoleViewer || required == managementRoleOperator
	case managementRoleViewer:
		return required == managementRoleViewer
	default:
		return false
	}
}

func (api *ManagementAPI) managementSecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.RLock()
		var security *config.SecurityConfig
		if api.security != nil {
			copy := *api.security
			copy.BasicAuthUsers = cloneBasicAuthUsers(api.security.BasicAuthUsers)
			copy.BasicAuthRoles = cloneBasicAuthRoles(api.security.BasicAuthRoles)
			security = &copy
		}
		api.mu.RUnlock()

		if security == nil || !security.EnableBasicAuth || !hasConfiguredBasicAuthUser(security.BasicAuthUsers) {
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

		requiredRole := managementRequiredRole(r.Method, r.URL.Path)
		role := security.BasicAuthRoles[username]
		if role == "" {
			// Backward compatibility: existing authenticated operators retain
			// their previous full-management access until roles are configured.
			role = managementRoleAdmin
		}
		if requiredRole == "" || !managementRoleAllows(role, requiredRole) {
			if api.logger != nil {
				api.logger.Warn("Management authorization denied",
					zap.String("username", username),
					zap.String("role", role),
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.String("required_role", requiredRole),
				)
			}
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if requiredRole != managementRoleViewer && api.logger != nil {
			api.logger.Info("Management mutation authorized",
				zap.String("username", username),
				zap.String("role", role),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
			)
		}
		next.ServeHTTP(w, r)
	})
}
