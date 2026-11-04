package gateway

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// validateNumouAccessTokenClaims enforces the machine-token claim contract issued by Numou Identity.
// Signature and registered JWT time-claim verification must already have succeeded.
func validateNumouAccessTokenClaims(claims jwt.MapClaims, expectedIssuer, expectedAudience string, now time.Time) error {
	if expectedIssuer == "" || expectedAudience == "" {
		return errors.New("Numou issuer and audience must be configured")
	}
	if stringClaim(claims, "iss") != expectedIssuer {
		return errors.New("invalid issuer")
	}
	if !contains(stringListClaim(claims, "aud"), expectedAudience) {
		return errors.New("invalid audience")
	}
	clientID := stringClaim(claims, "client_id")
	if clientID == "" || stringClaim(claims, "sub") != clientID {
		return errors.New("invalid client subject")
	}
	if stringClaim(claims, "token_use") != "access" {
		return errors.New("invalid token use")
	}
	uuidPattern := regexp.MustCompile("(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
	if !uuidPattern.MatchString(stringClaim(claims, "tenant_id")) ||
		!uuidPattern.MatchString(stringClaim(claims, "application_id")) {
		return errors.New("invalid tenant or application claim")
	}
	version, ok := numericClaim(claims["credential_version"])
	if !ok || version < 1 || version != float64(int64(version)) {
		return errors.New("invalid credential version")
	}
	expires, ok := numericClaim(claims["exp"])
	if !ok || expires <= float64(now.Unix()) {
		return errors.New("missing or expired token expiry")
	}
	scopes := strings.Fields(stringClaim(claims, "scope"))
	if len(scopes) == 0 || len(scopes) > 64 {
		return errors.New("invalid token scopes")
	}
	scopePattern := regexp.MustCompile("^[a-z][a-z0-9]*(?:[.:][a-z0-9][a-z0-9-]*)*$")
	for _, scope := range scopes {
		if len(scope) > 128 || !scopePattern.MatchString(scope) {
			return errors.New("invalid token scope")
		}
	}
	return nil
}
