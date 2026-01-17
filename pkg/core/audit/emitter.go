package audit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/logging"
)

// Emitter emits audit events to sinks
type Emitter struct {
	sinks   []Sink
	logger  *logging.Logger
	enabled bool

	// Buffer for async emission
	buffer     chan *Event
	bufferSize int

	// Filtering
	minSeverity Severity

	mu sync.RWMutex
	wg sync.WaitGroup
}

// NewEmitter creates a new audit emitter
func NewEmitter(logger *logging.Logger) *Emitter {
	return &Emitter{
		sinks:       make([]Sink, 0),
		logger:      logger,
		enabled:     true,
		bufferSize:  1000,
		minSeverity: SeverityInfo,
		buffer:      make(chan *Event, 1000),
	}
}

// AddSink registers an audit sink
func (e *Emitter) AddSink(sink Sink) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.sinks = append(e.sinks, sink)

	e.logger.Info("Audit sink added",
		logging.String("sink", sink.Name()),
	)
}

// Start starts the emitter
func (e *Emitter) Start() {
	e.wg.Add(1)
	go e.processEvents()

	e.logger.Info("Audit emitter started",
		logging.Int("buffer_size", e.bufferSize),
	)
}

// Stop stops the emitter
func (e *Emitter) Stop(ctx context.Context) error {
	close(e.buffer)

	// Wait for buffer to drain or context to cancel
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		e.logger.Info("Audit emitter stopped")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("audit emitter shutdown timeout")
	}
}

// Emit emits an audit event
func (e *Emitter) Emit(event *Event) error {
	if !e.enabled {
		return nil
	}

	// Check severity filter
	if !e.shouldEmit(event) {
		return nil
	}

	// Set timestamp if not set
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Generate ID if not set
	if event.ID == "" {
		event.ID = generateEventID()
	}

	// Try to send to buffer (non-blocking)
	select {
	case e.buffer <- event:
		return nil
	default:
		e.logger.Warn("Audit buffer full, dropping event",
			logging.String("event_type", string(event.Type)),
		)
		return fmt.Errorf("audit buffer full")
	}
}

// EmitSync emits an event synchronously
func (e *Emitter) EmitSync(event *Event) error {
	if !e.enabled {
		return nil
	}

	if !e.shouldEmit(event) {
		return nil
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	if event.ID == "" {
		event.ID = generateEventID()
	}

	return e.emitToSinks(event)
}

// processEvents processes events from the buffer
func (e *Emitter) processEvents() {
	defer e.wg.Done()

	for event := range e.buffer {
		if err := e.emitToSinks(event); err != nil {
			e.logger.Error("Failed to emit audit event",
				err,
				logging.String("event_id", event.ID),
				logging.String("event_type", string(event.Type)),
			)
		}
	}
}

// emitToSinks sends an event to all sinks
func (e *Emitter) emitToSinks(event *Event) error {
	e.mu.RLock()
	sinks := e.sinks
	e.mu.RUnlock()

	var lastErr error
	for _, sink := range sinks {
		if err := sink.Write(event); err != nil {
			e.logger.Error("Sink write failed",
				err,
				logging.String("sink", sink.Name()),
				logging.String("event_id", event.ID),
			)
			lastErr = err
		}
	}

	return lastErr
}

// shouldEmit checks if an event should be emitted
func (e *Emitter) shouldEmit(event *Event) bool {
	return severityLevel(event.Severity) >= severityLevel(e.minSeverity)
}
