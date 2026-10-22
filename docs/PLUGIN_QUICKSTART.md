# Plugin Quick Start Guide

This guide will help you create your first plugin for the Iket API Gateway in under 10 minutes.

## Prerequisites

- Go 1.21 or later
- Basic understanding of Go and HTTP middleware
- Iket API Gateway installed

## Step 1: Create Plugin Directory

```bash
mkdir my-first-plugin
cd my-first-plugin
go mod init my-first-plugin
```

## Step 2: Create Your Plugin

Create a file called `plugin.go`:

```go
package main

import (
    "fmt"
    "time"
    "github.com/bhangun/iket/pkg/core/engine"
    "github.com/bhangun/iket/pkg/logging"
)

// MyFirstPlugin implements a simple request logger
type MyFirstPlugin struct {
    enabled bool
    logger  *logging.Logger
}

func NewMyFirstPlugin(logger *logging.Logger) *MyFirstPlugin {
    return &MyFirstPlugin{logger: logger}
}

// Name returns the unique identifier
func (p *MyFirstPlugin) Name() string {
    return "my_first_plugin"
}

// Phase returns the execution phase (Rewrite, Auth, Traffic, etc.)
func (p *MyFirstPlugin) Phase() engine.Phase {
    return engine.PhaseTraffic
}

// Priority determines execution order (higher = earlier)
func (p *MyFirstPlugin) Priority() int {
    return 100
}

// Initialize sets up the plugin with configuration
func (p *MyFirstPlugin) Initialize(config map[string]interface{}) error {
    p.enabled = true
    if enabled, ok := config["enabled"].(bool); ok {
        p.enabled = enabled
    }
    return nil
}

// Execute processes the request using the ExecutionContext
func (p *MyFirstPlugin) Execute(ctx *engine.ExecutionContext) error {
    if !p.enabled {
        return nil
    }
    
    p.logger.Info("Hello from MyFirstPlugin!", 
        logging.String("request_id", ctx.RequestID),
        logging.String("path", ctx.RequestPath),
    )
    
    return nil
}

// OnFailure defines behavior on error
func (p *MyFirstPlugin) OnFailure() engine.FailureMode {
    return engine.FailureModeFailOpen
}

// Timeout returns the max execution time
func (p *MyFirstPlugin) Timeout() time.Duration {
    return 1 * time.Second
}
```

## Step 3: Build the Plugin

```bash
go build -buildmode=plugin -o my-first-plugin.so plugin.go
```

## Step 4: Configure the Gateway

Create a configuration file `config.yaml`:

```yaml
server:
  port: 8080
  plugins_dir: "./plugins"  # Directory where your .so files are located

plugins:
  my_first_plugin:
    enabled: true

routes:
  - path: "/api/*"
    destination: "http://localhost:3000"
    methods: ["GET", "POST", "PUT", "DELETE"]
```

## Step 5: Load the Plugin

1. Create the plugins directory:
```bash
mkdir plugins
```

2. Copy your plugin:
```bash
cp my-first-plugin.so plugins/
```

3. Start the gateway:
```bash
iket -config config.yaml
```

## Step 6: Test Your Plugin

Make a request to your gateway:

```bash
curl http://localhost:8080/api/test
```

You should see:
- The request logged in the gateway console
- A custom header `X-My-Plugin: Hello from MyFirstPlugin!` in the response

## Step 7: Verify Plugin Status

Check the plugin status via the admin endpoint:

```bash
curl -u admin:password http://localhost:8080/admin/plugins/status
```

## Common Issues and Solutions

### Issue: Plugin not loading
**Solution:** Check that:
- The `.so` file is in the correct plugins directory
- The plugin implements the required interfaces
- The plugin name matches the configuration

### Issue: Configuration not applied
**Solution:** Verify that:
- The configuration format is correct
- The plugin name in config matches the plugin's `Name()` method
- The plugin is properly initialized

### Issue: Middleware not executing
**Solution:** Ensure that:
- The plugin is enabled in configuration
- The plugin is registered with the gateway
- There are no errors in the gateway logs

## Next Steps

Now that you have a working plugin, you can:

1. **Add more functionality** - Implement request/response transformation
2. **Add configuration options** - Support more configuration parameters
3. **Implement health checks** - Add proper health monitoring
4. **Add metrics** - Track plugin performance
5. **Create tests** - Add unit and integration tests

## Example: Enhanced Plugin

Here's an enhanced version with more features:

```go
package main

import (
    "encoding/json"
    "fmt"
    "net/http"
    "sync"
    "time"
)

type EnhancedPlugin struct {
    enabled      bool
    logRequests  bool
    addHeader    string
    requestCount int64
    mu           sync.RWMutex
}

func (p *EnhancedPlugin) Name() string {
    return "enhanced_plugin"
}

func (p *EnhancedPlugin) Initialize(config map[string]interface{}) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    p.enabled = true
    p.logRequests = true
    p.addHeader = "Enhanced-Plugin"
    
    if enabled, ok := config["enabled"].(bool); ok {
        p.enabled = enabled
    }
    
    if logRequests, ok := config["log_requests"].(bool); ok {
        p.logRequests = logRequests
    }
    
    if header, ok := config["add_header"].(string); ok {
        p.addHeader = header
    }
    
    return nil
}

func (p *EnhancedPlugin) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !p.enabled {
            next.ServeHTTP(w, r)
            return
        }
        
        start := time.Now()
        
        // Log request if enabled
        if p.logRequests {
            fmt.Printf("[%s] %s %s\n", time.Now().Format("2006-01-02 15:04:05"), r.Method, r.URL.Path)
        }
        
        // Add custom header
        w.Header().Set("X-Enhanced-Plugin", p.addHeader)
        
        // Track request count
        p.mu.Lock()
        p.requestCount++
        p.mu.Unlock()
        
        // Continue to next handler
        next.ServeHTTP(w, r)
        
        // Log response time
        duration := time.Since(start)
        fmt.Printf("Request completed in %v\n", duration)
    })
}

func (p *EnhancedPlugin) Type() string {
    return "transform"
}

func (p *EnhancedPlugin) Tags() map[string]string {
    return map[string]string{
        "type":       "enhanced_plugin",
        "category":   "example",
        "middleware": "true",
    }
}

func (p *EnhancedPlugin) Health() error {
    if !p.enabled {
        return fmt.Errorf("plugin is disabled")
    }
    return nil
}

func (p *EnhancedPlugin) Status() string {
    p.mu.RLock()
    defer p.mu.RUnlock()
    
    if !p.enabled {
        return "EnhancedPlugin: Disabled"
    }
    
    return fmt.Sprintf("EnhancedPlugin: Enabled (requests: %d, logging: %t)", 
        p.requestCount, p.logRequests)
}

var Plugin = &EnhancedPlugin{}
```

Configuration for the enhanced plugin:

```yaml
plugins:
  enhanced_plugin:
    enabled: true
    log_requests: true
    add_header: "My Custom Header"
```

## Tips for Success

1. **Start simple** - Begin with basic functionality and add features incrementally
2. **Test thoroughly** - Test your plugin with different configurations and scenarios
3. **Handle errors gracefully** - Always check for errors and provide meaningful error messages
4. **Use logging** - Add logging to help debug issues
5. **Follow patterns** - Look at existing plugins for patterns and best practices
6. **Document your plugin** - Include comments and documentation for your plugin

## Resources

- [Plugin Development Guide](PLUGIN_DEVELOPMENT.md) - Comprehensive guide
- [Plugin Examples](PLUGIN_EXAMPLES.md) - More examples and patterns
- [Built-in Plugins](../internal/plugin/) - Reference implementations

Congratulations! You've created your first Iket plugin. Now you can extend the gateway's functionality with your custom logic. 