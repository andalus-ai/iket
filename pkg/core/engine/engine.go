package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/logging"
)

// FailureMode determines how to handle plugin failures
type FailureMode string

const (
	FailureModeDeny     FailureMode = "deny"      // Block request
	FailureModeFailOpen FailureMode = "fail_open" // Allow request
	// FailureModeRetry removed for now - implement when needed
)

// ExecutionContext holds the request state across phases
type ExecutionContext struct {
	context.Context

	// Request metadata
	RequestID string
	StartTime time.Time
	ClientIP  string
	RouteID   string

	// Phase execution state
	CurrentPhase Phase
	PhaseResults map[Phase]*PhaseResult

	// Shared state between plugins (phase-isolated access)
	state   map[string]interface{}
	stateMu sync.RWMutex

	// Error handling
	LastError error
}

// Engine is the PURE execution engine (no registry, no config)
type Engine struct {
	logger *logging.Logger

	// Default plugin execution timeout
	defaultTimeout time.Duration

	// Metrics
	metricsCollector MetricsCollector
}

// NewEngine creates a new phase-based execution engine
func NewEngine(logger *logging.Logger, metrics MetricsCollector) *Engine {
	return &Engine{
		logger:           logger,
		defaultTimeout:   30 * time.Second,
		metricsCollector: metrics,
	}
}

// SetDefaultTimeout configures the default plugin timeout
func (e *Engine) SetDefaultTimeout(timeout time.Duration) {
	e.defaultTimeout = timeout
}

// Execute runs the complete compiled pipeline
func (e *Engine) Execute(ctx *ExecutionContext, pipeline CompiledPipeline) error {
	// Initialize execution context
	ctx.PhaseResults = make(map[Phase]*PhaseResult)
	ctx.state = make(map[string]interface{})

	// Execute each phase in order
	for _, compiledPhase := range pipeline {
		ctx.CurrentPhase = compiledPhase.Phase

		result, err := e.executePhase(ctx, compiledPhase)
		ctx.PhaseResults[compiledPhase.Phase] = result

		if err != nil {
			e.logger.Error("Phase execution failed",
				err,
				logging.String("phase", string(compiledPhase.Phase)),
				logging.String("request_id", ctx.RequestID),
			)

			// Check failure mode
			if result.FailureMode == FailureModeDeny {
				return fmt.Errorf("phase %s failed (deny): %w", compiledPhase.Phase, err)
			}

			// fail_open: log error but continue
			e.logger.Warn("Phase failed but continuing (fail_open)",
				logging.String("phase", string(compiledPhase.Phase)),
				logging.String("request_id", ctx.RequestID),
			)
		}

		// Record phase metrics
		if e.metricsCollector != nil {
			e.metricsCollector.RecordPhaseExecution(
				compiledPhase.Phase,
				result.Duration,
				result.Success,
			)
		}
	}

	return nil
}

// executePhase runs all plugins in a specific phase
func (e *Engine) executePhase(ctx *ExecutionContext, phase CompiledPhase) (*PhaseResult, error) {
	start := time.Now()
	result := &PhaseResult{
		Phase:         phase.Phase,
		Success:       true,
		PluginResults: make([]*PluginResult, 0, len(phase.Plugins)),
	}

	for _, plugin := range phase.Plugins {
		pluginResult := e.executePlugin(ctx, plugin)
		result.PluginResults = append(result.PluginResults, pluginResult)

		if !pluginResult.Success {
			result.Success = false
			result.Error = pluginResult.Error
			result.FailureMode = plugin.OnFailure()

			// Stop phase execution if plugin failed with deny mode
			if plugin.OnFailure() == FailureModeDeny {
				break
			}

			// fail_open: log and continue
			e.logger.Warn("Plugin failed but continuing (fail_open)",
				logging.String("plugin", plugin.Name()),
				logging.String("phase", string(phase.Phase)),
				logging.String("request_id", ctx.RequestID),
			)
		}

		// Record plugin metrics
		if e.metricsCollector != nil {
			e.metricsCollector.RecordPluginExecution(
				plugin.Name(),
				phase.Phase,
				pluginResult.Duration,
				pluginResult.Success,
			)
		}
	}

	result.Duration = time.Since(start)
	return result, result.Error
}

// executePlugin runs a single plugin with isolation and timeout
func (e *Engine) executePlugin(ctx *ExecutionContext, plugin PhasePlugin) *PluginResult {
	start := time.Now()
	result := &PluginResult{
		PluginName: plugin.Name(),
		Success:    true,
		Metadata:   make(map[string]interface{}),
	}

	// Get plugin timeout
	timeout := plugin.Timeout()
	if timeout == 0 {
		timeout = e.defaultTimeout
	}

	// Only use goroutine if timeout is needed
	if timeout > 0 {
		result.Error = e.executeWithTimeout(ctx, plugin, timeout)
	} else {
		result.Error = e.executeDirect(ctx, plugin)
	}

	if result.Error != nil {
		result.Success = false

		e.logger.Error("Plugin execution failed",
			result.Error,
			logging.String("plugin", plugin.Name()),
			logging.String("phase", string(ctx.CurrentPhase)),
			logging.String("request_id", ctx.RequestID),
		)
	}

	result.Duration = time.Since(start)
	return result
}

// executeDirect runs plugin without goroutine (performance optimization)
func (e *Engine) executeDirect(ctx *ExecutionContext, plugin PhasePlugin) error {
	defer func() {
		if r := recover(); r != nil {
			e.logger.Error("Plugin panic",
				fmt.Errorf("panic: %v", r),
				logging.String("plugin", plugin.Name()),
				logging.String("request_id", ctx.RequestID),
			)
		}
	}()

	return plugin.Execute(ctx)
}

// executeWithTimeout runs plugin with timeout (uses goroutine only when needed)
func (e *Engine) executeWithTimeout(ctx *ExecutionContext, plugin PhasePlugin, timeout time.Duration) error {
	// Create timeout context
	execCtx, cancel := context.WithTimeout(ctx.Context, timeout)
	defer cancel()

	// Create isolated execution context
	isolatedCtx := &ExecutionContext{
		Context:      execCtx,
		RequestID:    ctx.RequestID,
		StartTime:    ctx.StartTime,
		ClientIP:     ctx.ClientIP,
		RouteID:      ctx.RouteID,
		CurrentPhase: ctx.CurrentPhase,
		state:        ctx.state, // Shared state (safe for now, add write barriers later)
		PhaseResults: ctx.PhaseResults,
	}

	// Execute with panic recovery
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("plugin panic: %v", r)
			}
		}()
		done <- plugin.Execute(isolatedCtx)
	}()

	// Wait for completion or timeout
	select {
	case err := <-done:
		return err
	case <-execCtx.Done():
		return fmt.Errorf("plugin timeout after %s", timeout)
	}
}

// NewExecutionContext creates a new execution context
func NewExecutionContext(ctx context.Context, requestID, clientIP, routeID string) *ExecutionContext {
	return &ExecutionContext{
		Context:      ctx,
		RequestID:    requestID,
		StartTime:    time.Now(),
		ClientIP:     clientIP,
		RouteID:      routeID,
		PhaseResults: make(map[Phase]*PhaseResult),
		state:        make(map[string]interface{}),
	}
}

// GetState retrieves a value from the execution state (thread-safe)
func (c *ExecutionContext) GetState(key string) (interface{}, bool) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	val, ok := c.state[key]
	return val, ok
}

// SetState stores a value in the execution state (thread-safe)
func (c *ExecutionContext) SetState(key string, value interface{}) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.state[key] = value
}

// GetPhaseResult retrieves the result of a specific phase
func (c *ExecutionContext) GetPhaseResult(phase Phase) (*PhaseResult, bool) {
	result, ok := c.PhaseResults[phase]
	return result, ok
}

// Duration returns total execution time so far
func (c *ExecutionContext) Duration() time.Duration {
	return time.Since(c.StartTime)
}

// IsTimedOut checks if the context has timed out
func (c *ExecutionContext) IsTimedOut() bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

// ExecutionStats returns summary statistics for this execution
type ExecutionStats struct {
	TotalDuration   time.Duration
	PhaseCount      int
	PluginCount     int
	SuccessCount    int
	FailureCount    int
	FailedPhases    []Phase
	SlowestPhase    Phase
	SlowestDuration time.Duration
}

// GetStats computes execution statistics
func (c *ExecutionContext) GetStats() *ExecutionStats {
	stats := &ExecutionStats{
		TotalDuration: c.Duration(),
		FailedPhases:  make([]Phase, 0),
	}

	for phase, result := range c.PhaseResults {
		stats.PhaseCount++
		stats.PluginCount += len(result.PluginResults)

		if result.Success {
			stats.SuccessCount++
		} else {
			stats.FailureCount++
			stats.FailedPhases = append(stats.FailedPhases, phase)
		}

		// Track slowest phase
		if result.Duration > stats.SlowestDuration {
			stats.SlowestDuration = result.Duration
			stats.SlowestPhase = phase
		}
	}

	return stats
}

// DryRunMode enables dry-run execution (no upstream calls)
type DryRunMode struct {
	Enabled      bool
	SkipPhases   []Phase // Phases to skip in dry-run
	MockUpstream bool    // Mock upstream responses
}

// ExecuteWithDryRun runs pipeline in dry-run mode
func (e *Engine) ExecuteWithDryRun(ctx *ExecutionContext, pipeline CompiledPipeline, dryRun *DryRunMode) error {
	if !dryRun.Enabled {
		return e.Execute(ctx, pipeline)
	}

	// Create filtered pipeline
	filteredPipeline := make(CompiledPipeline, 0)
	for _, phase := range pipeline {
		skip := false
		for _, skipPhase := range dryRun.SkipPhases {
			if phase.Phase == skipPhase {
				skip = true
				break
			}
		}

		if !skip {
			filteredPipeline = append(filteredPipeline, phase)
		}
	}

	// Mark as dry-run
	ctx.SetState("dry_run", true)

	return e.Execute(ctx, filteredPipeline)
}
