package traffic

import (
	"fmt"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
)

// CircuitBreakerPlugin implements circuit breaker pattern
type CircuitBreakerPlugin struct {
	// Base plugin properties
	name     string
	priority int
	timeout  time.Duration
	failure  engine.FailureMode

	// Circuit breaker configuration
	enabled             bool
	threshold           int           // Failures before opening
	halfOpenTimeout     time.Duration // Time before transitioning to half-open
	halfOpenMaxRequests int           // Max requests in half-open state
	windowDuration      time.Duration // Sliding window duration

	// Per-upstream circuit state
	circuits   map[string]*Circuit
	circuitsMu sync.RWMutex

	// Callbacks
	onStateChange func(upstream string, oldState, newState CircuitState)

	logger *logging.Logger
}

// CircuitState represents the circuit breaker state
type CircuitState string

const (
	StateClosed   CircuitState = "closed"
	StateOpen     CircuitState = "open"
	StateHalfOpen CircuitState = "half_open"
)

// Circuit holds the state for a single circuit
type Circuit struct {
	state            CircuitState
	failureCount     int
	successCount     int
	halfOpenRequests int
	lastFailureTime  time.Time
	lastStateChange  time.Time

	// Sliding window for failure tracking
	window *SlidingWindow

	mu sync.RWMutex
}

// SlidingWindow tracks events within a time window
type SlidingWindow struct {
	events   []time.Time
	duration time.Duration
	mu       sync.RWMutex
}

// NewCircuitBreakerPlugin creates a new circuit breaker plugin
func NewCircuitBreakerPlugin(logger *logging.Logger) *CircuitBreakerPlugin {
	return &CircuitBreakerPlugin{
		name:                "circuit_breaker",
		priority:            50,
		timeout:             1 * time.Second,
		failure:             engine.FailureModeFailOpen,
		threshold:           5,
		halfOpenTimeout:     30 * time.Second,
		halfOpenMaxRequests: 3,
		windowDuration:      60 * time.Second,
		circuits:            make(map[string]*Circuit),
		logger:              logger,
	}
}

// Plugin interface implementation

func (p *CircuitBreakerPlugin) Name() string {
	return p.name
}

func (p *CircuitBreakerPlugin) Phase() engine.Phase {
	return engine.PhaseTraffic
}

func (p *CircuitBreakerPlugin) Priority() int {
	return p.priority
}

func (p *CircuitBreakerPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *CircuitBreakerPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *CircuitBreakerPlugin) Initialize(config map[string]interface{}) error {
	// Enabled flag
	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	}

	if !p.enabled {
		return nil
	}

	// Threshold
	if threshold, ok := config["threshold"].(float64); ok {
		p.threshold = int(threshold)
	} else if threshold, ok := config["threshold"].(int); ok {
		p.threshold = threshold
	}

	// Half-open timeout
	if timeout, ok := config["half_open_timeout"].(float64); ok {
		p.halfOpenTimeout = time.Duration(timeout) * time.Second
	}

	// Half-open max requests
	if maxReq, ok := config["half_open_max_requests"].(float64); ok {
		p.halfOpenMaxRequests = int(maxReq)
	} else if maxReq, ok := config["half_open_max_requests"].(int); ok {
		p.halfOpenMaxRequests = maxReq
	}

	// Window duration
	if window, ok := config["window_duration"].(float64); ok {
		p.windowDuration = time.Duration(window) * time.Second
	}

	// Validate
	if p.threshold <= 0 {
		return fmt.Errorf("threshold must be positive")
	}

	if p.halfOpenMaxRequests <= 0 {
		return fmt.Errorf("half_open_max_requests must be positive")
	}

	p.logger.Info("Circuit breaker plugin initialized",
		logging.Int("threshold", p.threshold),
		logging.Duration("half_open_timeout", p.halfOpenTimeout),
		logging.Int("half_open_max_requests", p.halfOpenMaxRequests),
	)

	return nil
}

func (p *CircuitBreakerPlugin) Execute(ctx *engine.ExecutionContext) error {
	if !p.enabled {
		return nil
	}

	// Get upstream from context
	upstream := ctx.RouteID // Using route as circuit identifier

	// Get or create circuit
	circuit := p.getCircuit(upstream)

	// Check circuit state
	if !circuit.allowRequest() {
		p.logger.Warn("Circuit breaker open",
			logging.String("upstream", upstream),
			logging.String("request_id", ctx.RequestID),
			logging.String("state", string(circuit.state)),
		)

		ctx.SetState("circuit_open", true)
		ctx.SetState("circuit_upstream", upstream)

		return fmt.Errorf("circuit breaker open for %s", upstream)
	}

	// Store circuit in context for result tracking
	ctx.SetState("circuit", circuit)
	ctx.SetState("circuit_upstream", upstream)

	return nil
}

// getCircuit gets or creates a circuit for an upstream
func (p *CircuitBreakerPlugin) getCircuit(upstream string) *Circuit {
	p.circuitsMu.RLock()
	circuit, exists := p.circuits[upstream]
	p.circuitsMu.RUnlock()

	if exists {
		return circuit
	}

	p.circuitsMu.Lock()
	defer p.circuitsMu.Unlock()

	// Double-check
	if circuit, exists := p.circuits[upstream]; exists {
		return circuit
	}

	circuit = &Circuit{
		state:           StateClosed,
		lastStateChange: time.Now(),
		window:          NewSlidingWindow(p.windowDuration),
	}

	p.circuits[upstream] = circuit

	p.logger.Debug("Created new circuit",
		logging.String("upstream", upstream),
	)

	return circuit
}

// RecordSuccess records a successful request
func (p *CircuitBreakerPlugin) RecordSuccess(ctx *engine.ExecutionContext) {
	circuit, ok := ctx.GetState("circuit")
	if !ok {
		return
	}

	c := circuit.(*Circuit)
	upstream, _ := ctx.GetState("circuit_upstream")

	c.onSuccess()

	// Check for state transition
	if c.state == StateHalfOpen {
		p.logger.Info("Circuit transitioning to closed",
			logging.String("upstream", upstream.(string)),
		)

		if p.onStateChange != nil {
			p.onStateChange(upstream.(string), StateHalfOpen, StateClosed)
		}
	}
}

// RecordFailure records a failed request
func (p *CircuitBreakerPlugin) RecordFailure(ctx *engine.ExecutionContext) {
	circuit, ok := ctx.GetState("circuit")
	if !ok {
		return
	}

	c := circuit.(*Circuit)
	upstream, _ := ctx.GetState("circuit_upstream")

	oldState := c.state
	c.onFailure(p.threshold)
	newState := c.state

	if oldState != newState {
		p.logger.Warn("Circuit state changed",
			logging.String("upstream", upstream.(string)),
			logging.String("old_state", string(oldState)),
			logging.String("new_state", string(newState)),
			logging.Int("failures", c.failureCount),
		)

		if p.onStateChange != nil {
			p.onStateChange(upstream.(string), oldState, newState)
		}
	}
}

// Circuit methods

func (c *Circuit) allowRequest() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	switch c.state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if it's time to transition to half-open
		// Note: halfOpenTimeout should be passed from plugin, hardcoded here for simplicity
		if now.Sub(c.lastFailureTime) > 30*time.Second {
			c.state = StateHalfOpen
			c.halfOpenRequests = 0
			c.lastStateChange = now
			return true
		}
		return false

	case StateHalfOpen:
		// Allow limited requests in half-open state
		if c.halfOpenRequests < 3 { // halfOpenMaxRequests
			c.halfOpenRequests++
			return true
		}
		return false

	default:
		return false
	}
}

func (c *Circuit) onSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.successCount++

	switch c.state {
	case StateHalfOpen:
		// After successful requests in half-open, transition to closed
		if c.successCount >= 3 { // Should be configurable
			c.state = StateClosed
			c.failureCount = 0
			c.successCount = 0
			c.halfOpenRequests = 0
			c.window.Clear()
			c.lastStateChange = time.Now()
		}

	case StateClosed:
		// Reset failure count on success
		c.failureCount = 0
	}
}

func (c *Circuit) onFailure(threshold int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	c.lastFailureTime = now
	c.failureCount++

	// Add to sliding window
	c.window.Add(now)

	switch c.state {
	case StateClosed:
		// Check if failures exceed threshold in window
		if c.window.Count() >= threshold {
			c.state = StateOpen
			c.lastStateChange = now
		}

	case StateHalfOpen:
		// Any failure in half-open returns to open
		c.state = StateOpen
		c.halfOpenRequests = 0
		c.successCount = 0
		c.lastStateChange = now
	}
}

func (c *Circuit) GetState() CircuitState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Circuit) GetStats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return map[string]interface{}{
		"state":              string(c.state),
		"failure_count":      c.failureCount,
		"success_count":      c.successCount,
		"window_failures":    c.window.Count(),
		"half_open_requests": c.halfOpenRequests,
		"last_state_change":  c.lastStateChange,
	}
}

// SlidingWindow methods

func NewSlidingWindow(duration time.Duration) *SlidingWindow {
	return &SlidingWindow{
		events:   make([]time.Time, 0),
		duration: duration,
	}
}

func (w *SlidingWindow) Add(t time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Remove old events
	cutoff := t.Add(-w.duration)
	validEvents := make([]time.Time, 0)

	for _, event := range w.events {
		if event.After(cutoff) {
			validEvents = append(validEvents, event)
		}
	}

	// Add new event
	validEvents = append(validEvents, t)
	w.events = validEvents
}

func (w *SlidingWindow) Count() int {
	w.mu.RLock()
	defer w.mu.RUnlock()

	now := time.Now()
	cutoff := now.Add(-w.duration)

	count := 0
	for _, event := range w.events {
		if event.After(cutoff) {
			count++
		}
	}

	return count
}

func (w *SlidingWindow) Clear() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = make([]time.Time, 0)
}

// Plugin management methods

func (p *CircuitBreakerPlugin) GetCircuitState(upstream string) (CircuitState, bool) {
	p.circuitsMu.RLock()
	defer p.circuitsMu.RUnlock()

	circuit, exists := p.circuits[upstream]
	if !exists {
		return "", false
	}

	return circuit.GetState(), true
}

func (p *CircuitBreakerPlugin) GetAllCircuits() map[string]CircuitState {
	p.circuitsMu.RLock()
	defer p.circuitsMu.RUnlock()

	states := make(map[string]CircuitState)
	for upstream, circuit := range p.circuits {
		states[upstream] = circuit.GetState()
	}

	return states
}

func (p *CircuitBreakerPlugin) ResetCircuit(upstream string) {
	p.circuitsMu.Lock()
	defer p.circuitsMu.Unlock()

	if circuit, exists := p.circuits[upstream]; exists {
		circuit.mu.Lock()
		circuit.state = StateClosed
		circuit.failureCount = 0
		circuit.successCount = 0
		circuit.halfOpenRequests = 0
		circuit.window.Clear()
		circuit.lastStateChange = time.Now()
		circuit.mu.Unlock()

		p.logger.Info("Circuit reset",
			logging.String("upstream", upstream),
		)
	}
}

func (p *CircuitBreakerPlugin) SetStateChangeCallback(callback func(upstream string, oldState, newState CircuitState)) {
	p.onStateChange = callback
}

func (p *CircuitBreakerPlugin) Health() error {
	if !p.enabled {
		return fmt.Errorf("circuit breaker plugin is disabled")
	}

	return nil
}

func (p *CircuitBreakerPlugin) GetStats() map[string]interface{} {
	p.circuitsMu.RLock()
	defer p.circuitsMu.RUnlock()

	stats := make(map[string]interface{})
	stats["total_circuits"] = len(p.circuits)

	stateCount := make(map[string]int)
	for _, circuit := range p.circuits {
		state := string(circuit.GetState())
		stateCount[state]++
	}
	stats["states"] = stateCount

	return stats
}
