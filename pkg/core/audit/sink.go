package audit

// Sink interface

// Sink is the interface that audit sinks must implement
type Sink interface {
	Name() string
	Write(event *Event) error
	Close() error
}
