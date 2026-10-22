package transform

import (
	"fmt"
	"net/http"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
)

// CORSPlugin handles Cross-Origin Resource Sharing
type CORSPlugin struct {
	name         string
	priority     int
	timeout      time.Duration
	failure      engine.FailureMode
	allowOrigin  string
	allowMethods string
	allowHeaders string
	logger       *logging.Logger
}

// NewCORSPlugin creates a new CORS plugin
func NewCORSPlugin(logger *logging.Logger) *CORSPlugin {
	return &CORSPlugin{
		name:         "cors",
		priority:     100, // Process early
		timeout:      500 * time.Millisecond,
		failure:      engine.FailureModeFailOpen, // CORS shouldn't block main logic if it fails to set headers
		allowOrigin:  "*",
		allowMethods: "GET,POST,PUT,DELETE,OPTIONS",
		allowHeaders: "Content-Type, Authorization",
		logger:       logger,
	}
}

func (p *CORSPlugin) Name() string { return p.name }

func (p *CORSPlugin) Phase() engine.Phase {
	return engine.PhaseRewrite // Run in rewrite phase to handle preflight early
}

func (p *CORSPlugin) Priority() int {
	return p.priority
}

func (p *CORSPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *CORSPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *CORSPlugin) Initialize(config map[string]interface{}) error {
	if v, ok := config["allow_origin"].(string); ok {
		p.allowOrigin = v
	}
	if v, ok := config["allow_methods"].(string); ok {
		p.allowMethods = v
	}
	if v, ok := config["allow_headers"].(string); ok {
		p.allowHeaders = v
	}

	p.logger.Info("CORS plugin initialized",
		logging.String("allow_origin", p.allowOrigin),
	)
	return nil
}

func (p *CORSPlugin) Execute(ctx *engine.ExecutionContext) error {
	// Extract HTTP request and response from context
	reqVal, ok := ctx.GetState("http_request")
	if !ok {
		return fmt.Errorf("http request not found in context")
	}
	r := reqVal.(*http.Request)

	respVal, ok := ctx.GetState("http_response")
	if !ok {
		return fmt.Errorf("http response not found in context")
	}
	w := respVal.(http.ResponseWriter)

	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", p.allowOrigin)
	w.Header().Set("Access-Control-Allow-Methods", p.allowMethods)
	w.Header().Set("Access-Control-Allow-Headers", p.allowHeaders)

	// Handle preflight
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		ctx.SetState("response_written", true)
		return nil
	}

	return nil
}
