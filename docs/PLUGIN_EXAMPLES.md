# Plugin Examples

This document provides practical examples of custom plugins for the Iket API Gateway using the `PhasePlugin` architecture.

## Example 1: Simple Request Counter Plugin

This plugin increments a counter for every request in the `Traffic` phase.

```go
package requestcounter

import (
    "sync/atomic"
    "time"

    "github.com/bhangun/iket/pkg/core/engine"
    "github.com/bhangun/iket/pkg/logging"
)

type RequestCounterPlugin struct {
    enabled      bool
    requestCount int64
    logger       *logging.Logger
}

func NewRequestCounterPlugin(logger *logging.Logger) *RequestCounterPlugin {
    return &RequestCounterPlugin{logger: logger}
}

func (p *RequestCounterPlugin) Name() string { return "request_counter" }
func (p *RequestCounterPlugin) Phase() engine.Phase { return engine.PhaseTraffic }
func (p *RequestCounterPlugin) Priority() int { return 100 }
func (p *RequestCounterPlugin) Timeout() time.Duration { return 1 * time.Second }
func (p *RequestCounterPlugin) OnFailure() engine.FailureMode { return engine.FailureModeFailOpen }

func (p *RequestCounterPlugin) Initialize(config map[string]interface{}) error {
    p.enabled = true
    if enabled, ok := config["enabled"].(bool); ok {
        p.enabled = enabled
    }
    return nil
}

func (p *RequestCounterPlugin) Execute(ctx *engine.ExecutionContext) error {
    if !p.enabled {
        return nil
    }

    // Increment counter
    count := atomic.AddInt64(&p.requestCount, 1)
    
    // Log the count
    p.logger.Info("Request count updated", 
        logging.Int64("count", count),
        logging.String("request_id", ctx.RequestID),
    )

    return nil
}
```

## Example 2: API Key Authentication Plugin

This plugin validates an API key provided in the header during the `Auth` phase.

```go
package apikey

import (
    "fmt"
    "time"

    "github.com/bhangun/iket/pkg/core/engine"
    "github.com/bhangun/iket/pkg/logging"
)

type APIKeyPlugin struct {
    enabled bool
    keys    map[string]string // key -> user mapping
    logger  *logging.Logger
}

func NewAPIKeyPlugin(logger *logging.Logger) *APIKeyPlugin {
    return &APIKeyPlugin{
        keys:   make(map[string]string),
        logger: logger,
    }
}

func (p *APIKeyPlugin) Name() string { return "api_key" }
func (p *APIKeyPlugin) Phase() engine.Phase { return engine.PhaseAuth }
func (p *APIKeyPlugin) Priority() int { return 90 }
func (p *APIKeyPlugin) Timeout() time.Duration { return 2 * time.Second }
func (p *APIKeyPlugin) OnFailure() engine.FailureMode { return engine.FailureModeDeny }

func (p *APIKeyPlugin) Initialize(config map[string]interface{}) error {
    p.enabled = true
    if enabled, ok := config["enabled"].(bool); ok {
        p.enabled = enabled
    }
    
    if keys, ok := config["keys"].(map[string]interface{}); ok {
        for k, v := range keys {
            if user, ok := v.(string); ok {
                p.keys[k] = user
            }
        }
    }
    return nil
}

func (p *APIKeyPlugin) Execute(ctx *engine.ExecutionContext) error {
    if !p.enabled {
        return nil
    }

    apiKey := ctx.Request.Header.Get("X-API-Key")
    if apiKey == "" {
        return fmt.Errorf("missing API key")
    }

    user, exists := p.keys[apiKey]
    if !exists {
        return fmt.Errorf("invalid API key")
    }

    // Store user info in context for downstream plugins
    ctx.SetState("user", user)
    
    p.logger.Debug("User authenticated", logging.String("user", user))
    return nil
}
```

## Example 3: Request Transformation Plugin

This plugin modifies request headers in the `Rewrite` phase.

```go
package transformer

import (
    "time"
    "github.com/bhangun/iket/pkg/core/engine"
)

type TransformerPlugin struct {
    enabled       bool
    addHeaders    map[string]string
    removeHeaders []string
}

func (p *TransformerPlugin) Name() string { return "transformer" }
func (p *TransformerPlugin) Phase() engine.Phase { return engine.PhaseRewrite }
func (p *TransformerPlugin) Priority() int { return 50 }
func (p *TransformerPlugin) Timeout() time.Duration { return 500 * time.Millisecond }
func (p *TransformerPlugin) OnFailure() engine.FailureMode { return engine.FailureModeFailOpen }

func (p *TransformerPlugin) Initialize(config map[string]interface{}) error {
    p.enabled = true
    p.addHeaders = make(map[string]string)
    
    if enabled, ok := config["enabled"].(bool); ok {
        p.enabled = enabled
    }
    
    if headers, ok := config["add_headers"].(map[string]interface{}); ok {
        for k, v := range headers {
            if val, ok := v.(string); ok {
                p.addHeaders[k] = val
            }
        }
    }
    
    if headers, ok := config["remove_headers"].([]interface{}); ok {
        for _, v := range headers {
            if val, ok := v.(string); ok {
                p.removeHeaders = append(p.removeHeaders, val)
            }
        }
    }
    
    return nil
}

func (p *TransformerPlugin) Execute(ctx *engine.ExecutionContext) error {
    if !p.enabled {
        return nil
    }

    // Add headers
    for k, v := range p.addHeaders {
        ctx.Request.Header.Set(k, v)
    }
    
    // Remove headers
    for _, k := range p.removeHeaders {
        ctx.Request.Header.Del(k)
    }

    return nil
}
```

## Example 4: Circuit Breaker Plugin (Monitoring)

This plugin monitors failures and can be used to fail requests if a threshold is reached.

```go
package circuitbreaker

import (
    "fmt"
    "sync"
    "time"

    "github.com/bhangun/iket/pkg/core/engine"
)

type CircuitBreakerPlugin struct {
    threshold    int
    failures     int
    state        string // "CLOSED", "OPEN"
    lastFailure  time.Time
    mu           sync.RWMutex
}

func (p *CircuitBreakerPlugin) Name() string { return "circuit_breaker" }
func (p *CircuitBreakerPlugin) Phase() engine.Phase { return engine.PhaseTraffic }
func (p *CircuitBreakerPlugin) Priority() int { return 10 }
func (p *CircuitBreakerPlugin) Timeout() time.Duration { return 100 * time.Millisecond }
func (p *CircuitBreakerPlugin) OnFailure() engine.FailureMode { return engine.FailureModeDeny }

func (p *CircuitBreakerPlugin) Initialize(config map[string]interface{}) error {
    p.threshold = 5
    if t, ok := config["threshold"].(int); ok {
        p.threshold = t
    }
    p.state = "CLOSED"
    return nil
}

func (p *CircuitBreakerPlugin) Execute(ctx *engine.ExecutionContext) error {
    p.mu.RLock()
    defer p.mu.RUnlock()

    if p.state == "OPEN" {
        // Check if we should try to close it (half-open logic omitted for brevity)
        if time.Since(p.lastFailure) > 30*time.Second {
            // Should transition to half-open
            return nil 
        }
        return fmt.Errorf("circuit breaker is open")
    }

    return nil
}

// Custom logic to be called on upstream failures
func (p *CircuitBreakerPlugin) RecordFailure() {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.failures++
    p.lastFailure = time.Now()
    if p.failures >= p.threshold {
        p.state = "OPEN"
    }
}
```

## Configuration Examples

### Plugin Configuration in `gateway.yaml`

```yaml
plugins:
  request_counter:
    enabled: true
  api_key:
    enabled: true
    keys:
      "secret-key-1": "admin"
      "user-key-2": "basic-user"
  transformer:
    enabled: true
    add_headers:
      "X-Iket-Transformed": "true"
    remove_headers:
      - "X-Sensitive"
```

## Building and Registering Plugins

Iket uses a factory pattern for plugin registration. Plugins are typically compiled as part of the gateway or registered in a central registry.

### 1. Define a Factory

```go
func NewRequestCounterFactory(logger *logging.Logger) plugin.Factory {
    return func() engine.PhasePlugin {
        return NewRequestCounterPlugin(logger)
    }
}
```

### 2. Register with the Registry

In your application's initialization (e.g., `pkg/core/plugin/plugin.go` or a dedicated plugin loader):

```go
registry := plugin.NewRegistry(logger)
registry.RegisterFactory("request_counter", NewRequestCounterFactory(logger))
```

## Testing Your Plugin

### Unit Testing `Execute`

```go
func TestRequestCounter_Execute(t *testing.T) {
    logger := logging.NewNopLogger()
    p := NewRequestCounterPlugin(logger)
    p.Initialize(map[string]interface{}{"enabled": true})

    req := httptest.NewRequest("GET", "/", nil)
    ctx := &engine.ExecutionContext{
        Request:   req,
        RequestID: "test-req",
    }

    err := p.Execute(ctx)
    if err != nil {
        t.Errorf("expected no error, got %v", err)
    }

    if p.requestCount != 1 {
        t.Errorf("expected count 1, got %d", p.requestCount)
    }
}
```

These examples provide a solid foundation for creating custom plugins using Iket's phase-based architecture. 