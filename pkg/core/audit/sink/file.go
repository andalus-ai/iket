package sink

import (
	"encoding/json"
	"sync"

	event "github.com/bhangun/iket/pkg/core/audit"
)

// FileSink writes audit events to a file

type FileSink struct {
	name     string
	filepath string
	encoder  *json.Encoder
	mu       sync.Mutex
}

func NewFileSink(filepath string) (*FileSink, error) {
	// In production, open file and create encoder
	return &FileSink{
		name:     "file",
		filepath: filepath,
	}, nil
}

func (s *FileSink) Name() string {
	return s.name
}

func (s *FileSink) Write(event *event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// In production, write to file
	// For now, just log
	return nil
}

func (s *FileSink) Close() error {
	// Close file
	return nil
}
