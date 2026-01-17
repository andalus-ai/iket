package engine

import "time"

// MetricsCollector defines the interface for collecting execution metrics
type MetricsCollector interface {
	RecordPhaseExecution(phase Phase, duration time.Duration, success bool)
	RecordPluginExecution(plugin string, phase Phase, duration time.Duration, success bool)
}
