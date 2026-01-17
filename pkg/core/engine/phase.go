package engine

import "time"

// Phase represents the execution phase in the request lifecycle
type Phase string

const (
	PhaseRewrite  Phase = "rewrite"
	PhaseAuth     Phase = "auth"
	PhasePolicy   Phase = "policy"
	PhaseTraffic  Phase = "traffic"
	PhaseUpstream Phase = "upstream"
	PhaseResponse Phase = "response"
	PhaseLog      Phase = "log"
)

// PhaseOrder defines the deterministic execution order
var PhaseOrder = []Phase{
	PhaseRewrite,
	PhaseAuth,
	PhasePolicy,
	PhaseTraffic,
	PhaseUpstream,
	PhaseResponse,
	PhaseLog,
}

// PhaseResult captures the outcome of a phase execution
type PhaseResult struct {
	Phase         Phase
	Success       bool
	Duration      time.Duration
	PluginResults []*PluginResult
	Error         error
	FailureMode   FailureMode
}

// PluginResult captures individual plugin execution
type PluginResult struct {
	PluginName string
	Success    bool
	Duration   time.Duration
	Error      error
	Metadata   map[string]interface{}
}

// PhasePlugin extends the base plugin with phase-aware capabilities
type PhasePlugin interface {
	// Name returns the plugin name
	Name() string

	// Phase returns which phase this plugin belongs to
	Phase() Phase

	// Priority determines execution order within a phase (higher = earlier)
	Priority() int

	// Execute runs the plugin logic
	Execute(ctx *ExecutionContext) error

	// OnFailure defines behavior when execution fails
	OnFailure() FailureMode

	// Initialize configures the plugin
	Initialize(config map[string]interface{}) error

	// Timeout returns the max execution time
	Timeout() time.Duration
}
