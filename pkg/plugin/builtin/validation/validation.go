package validation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
)

// ValidationPlugin implements request body validation
type ValidationPlugin struct {
	name       string
	priority   int
	timeout    time.Duration
	failure    engine.FailureMode
	enabled    bool
	strictMode bool
	skipPaths  []string
	logger     *logging.Logger
}

// NewValidationPlugin creates a new validation plugin
func NewValidationPlugin(logger *logging.Logger) *ValidationPlugin {
	return &ValidationPlugin{
		name:     "validation",
		priority: 80,
		timeout:  2 * time.Second,
		failure:  engine.FailureModeDeny,
		logger:   logger,
	}
}

func (p *ValidationPlugin) Name() string { return p.name }

func (p *ValidationPlugin) Phase() engine.Phase {
	return engine.PhaseTraffic // Validate before upstream
}

func (p *ValidationPlugin) Priority() int {
	return p.priority
}

func (p *ValidationPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *ValidationPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *ValidationPlugin) Initialize(config map[string]interface{}) error {
	if val, ok := config["enabled"].(bool); ok {
		p.enabled = val
	} else {
		p.enabled = true
	}

	if strict, ok := config["strict_mode"].(bool); ok {
		p.strictMode = strict
	}

	if skipPaths, ok := config["skip_paths"].([]interface{}); ok {
		for _, path := range skipPaths {
			if pathStr, ok := path.(string); ok {
				p.skipPaths = append(p.skipPaths, pathStr)
			}
		}
	}

	p.logger.Info("Validation plugin initialized",
		logging.Bool("strict_mode", p.strictMode),
		logging.Int("skip_paths", len(p.skipPaths)),
	)
	return nil
}

func (p *ValidationPlugin) Execute(ctx *engine.ExecutionContext) error {
	if !p.enabled {
		return nil
	}

	// Extract HTTP request
	reqVal, ok := ctx.GetState("http_request")
	if !ok {
		return fmt.Errorf("http request not found in context")
	}
	r := reqVal.(*http.Request)

	// Check if path should skip validation
	if p.shouldSkipValidation(r.URL.Path) {
		return nil
	}

	// Validate request
	if err := p.validateRequest(r); err != nil {
		p.logger.Warn("Validation failed",
			logging.Error(err),
			logging.String("path", r.URL.Path),
			logging.String("request_id", ctx.RequestID),
		)
		return err
	}

	return nil
}

func (p *ValidationPlugin) shouldSkipValidation(path string) bool {
	for _, skipPath := range p.skipPaths {
		if strings.HasPrefix(path, skipPath) {
			return true
		}
	}
	return false
}

func (p *ValidationPlugin) validateRequest(r *http.Request) error {
	// Only validate JSON requests
	contentType := r.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		return nil
	}

	// Validate request body
	if r.Body == nil {
		return nil
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("failed to read request body: %w", err)
	}

	// Restore body for downstream handlers
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	if len(body) == 0 {
		return nil
	}

	// Basic JSON validation
	var jsonData interface{}
	if err := json.Unmarshal(body, &jsonData); err != nil {
		return fmt.Errorf("invalid JSON in request body: %w", err)
	}

	return nil
}
