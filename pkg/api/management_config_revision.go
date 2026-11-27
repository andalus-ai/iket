package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bhangun/iket/pkg/config"
)

// configETag returns a strong, content-addressed entity tag for a configuration.
// JSON encoding is deterministic for Go map keys, making this suitable for
// optimistic concurrency across management API instances sharing the same config.
func configETag(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("configuration is nil")
	}
	payload, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return `"sha256:` + hex.EncodeToString(sum[:]) + `"`, nil
}

// ifMatchConfigETag supports the HTTP If-Match list and wildcard forms.
// Weak entity tags are intentionally not accepted because If-Match requires
// strong comparison.
func ifMatchConfigETag(header, current string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == current {
			return true
		}
	}
	return false
}
