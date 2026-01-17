package sink

import event "github.com/bhangun/iket/pkg/core/audit"

// NoopSink discards all events (for testing)
type NoopSink struct{}

func NewNoopSink() *NoopSink {
	return &NoopSink{}
}

func (s *NoopSink) Name() string {
	return "noop"
}

func (s *NoopSink) Write(event *event.Event) error {
	return nil
}

func (s *NoopSink) Close() error {
	return nil
}
