package audit

import (
	"fmt"
	"time"
)

// EventType categorizes audit events
type EventType string

const (
	EventConfigLoaded     EventType = "config.loaded"
	EventConfigUpdated    EventType = "config.updated"
	EventConfigRolledBack EventType = "config.rolled_back"
	EventConfigValidated  EventType = "config.validated"

	EventRouteAdded   EventType = "route.added"
	EventRouteRemoved EventType = "route.removed"
	EventRouteUpdated EventType = "route.updated"

	EventPluginEnabled  EventType = "plugin.enabled"
	EventPluginDisabled EventType = "plugin.disabled"
	EventPluginUpdated  EventType = "plugin.updated"

	EventAuthSuccess EventType = "auth.success"
	EventAuthFailure EventType = "auth.failure"

	EventPolicyAllowed EventType = "policy.allowed"
	EventPolicyDenied  EventType = "policy.denied"

	EventRateLimited EventType = "rate_limit.exceeded"

	EventCircuitOpened   EventType = "circuit.opened"
	EventCircuitClosed   EventType = "circuit.closed"
	EventCircuitHalfOpen EventType = "circuit.half_open"

	EventRequestProcessed EventType = "request.processed"
	EventRequestFailed    EventType = "request.failed"
)

// Severity indicates the importance of an event
type Severity string

const (
	SeverityDebug    Severity = "debug"
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

// Event represents an audit event
type Event struct {
	// Identification
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`

	// Classification
	Severity Severity `json:"severity"`
	Category string   `json:"category"`

	// Actor information
	Actor *Actor `json:"actor,omitempty"`

	// Target information
	Target *Target `json:"target,omitempty"`

	// Context
	Request *RequestContext `json:"request,omitempty"`

	// Event data
	Data    map[string]interface{} `json:"data,omitempty"`
	Message string                 `json:"message,omitempty"`

	// Metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Tags     []string               `json:"tags,omitempty"`
}

// Actor represents who performed the action
type Actor struct {
	Type      string `json:"type"` // user, service, system
	ID        string `json:"id"`
	Username  string `json:"username,omitempty"`
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

// Target represents what was affected
type Target struct {
	Type string `json:"type"` // config, route, plugin, etc.
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// RequestContext provides HTTP request context
type RequestContext struct {
	RequestID string `json:"request_id"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Route     string `json:"route,omitempty"`
	ClientIP  string `json:"client_ip"`
}

// Helper functions

func generateEventID() string {
	return fmt.Sprintf("evt_%d", time.Now().UnixNano())
}

func severityLevel(s Severity) int {
	levels := map[Severity]int{
		SeverityDebug:    0,
		SeverityInfo:     1,
		SeverityWarning:  2,
		SeverityError:    3,
		SeverityCritical: 4,
	}
	return levels[s]
}

// Event builders

// NewConfigEvent creates a config-related event
func NewConfigEvent(eventType EventType, actor *Actor, message string, data map[string]interface{}) *Event {
	return &Event{
		Type:     eventType,
		Severity: SeverityInfo,
		Category: "config",
		Actor:    actor,
		Message:  message,
		Data:     data,
		Metadata: make(map[string]interface{}),
		Tags:     []string{"config"},
	}
}

// NewAuthEvent creates an auth-related event
func NewAuthEvent(eventType EventType, reqCtx *RequestContext, success bool, reason string) *Event {
	severity := SeverityInfo
	if !success {
		severity = SeverityWarning
	}

	return &Event{
		Type:     eventType,
		Severity: severity,
		Category: "auth",
		Request:  reqCtx,
		Message:  reason,
		Data: map[string]interface{}{
			"success": success,
		},
		Tags: []string{"auth", "security"},
	}
}

// NewPolicyEvent creates a policy-related event
func NewPolicyEvent(eventType EventType, reqCtx *RequestContext, allowed bool, reason string) *Event {
	severity := SeverityInfo
	if !allowed {
		severity = SeverityWarning
	}

	return &Event{
		Type:     eventType,
		Severity: severity,
		Category: "policy",
		Request:  reqCtx,
		Message:  reason,
		Data: map[string]interface{}{
			"allowed": allowed,
		},
		Tags: []string{"policy", "authorization"},
	}
}

// NewRequestEvent creates a request-related event
func NewRequestEvent(reqCtx *RequestContext, statusCode int, duration time.Duration) *Event {
	eventType := EventRequestProcessed
	severity := SeverityInfo

	if statusCode >= 400 {
		eventType = EventRequestFailed
		if statusCode >= 500 {
			severity = SeverityError
		} else {
			severity = SeverityWarning
		}
	}

	return &Event{
		Type:     eventType,
		Severity: severity,
		Category: "request",
		Request:  reqCtx,
		Data: map[string]interface{}{
			"status_code": statusCode,
			"duration_ms": duration.Milliseconds(),
		},
		Tags: []string{"request"},
	}
}
