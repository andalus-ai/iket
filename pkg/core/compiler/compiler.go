package compiler

import (
	"fmt"
	"sort"
	"sync"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/core/provider"
	"github.com/bhangun/iket/pkg/logging"
	"github.com/bhangun/iket/pkg/plugin"
)

// CompiledConfig represents the final, execution-ready configuration
type CompiledConfig struct {
	Version  string
	Routes   []*CompiledRoute
	Globals  *GlobalConfig
	Metadata map[string]interface{}

	// Audit information
	CompiledAt int64
	CompiledBy string
}

// CompiledRoute represents a single route with its complete execution pipeline
type CompiledRoute struct {
	ID       string
	Path     string
	Methods  []string
	Upstream string

	// Compiled, immutable pipeline
	Pipeline engine.CompiledPipeline

	// Route metadata
	Metadata map[string]interface{}
	Priority int
	Enabled  bool
}

// GlobalConfig holds gateway-wide settings
type GlobalConfig struct {
	Port            int
	DefaultTimeout  int64
	MaxRequestSize  int64
	EnableMetrics   bool
	EnableTracing   bool
	SecurityHeaders map[string]string
}

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string
	Message string
	Value   interface{}
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error in %s: %s", e.Field, e.Message)
}

// Compiler transforms raw configuration into execution-ready compiled config
type Compiler struct {
	logger   *logging.Logger
	registry *plugin.Registry

	// Validation rules
	validators []Validator

	mu sync.RWMutex
}

// Validator defines the interface for configuration validation
type Validator interface {
	Validate(snapshot *provider.ConfigSnapshot) error
}

// NewCompiler creates a new configuration compiler
func NewCompiler(logger *logging.Logger, registry *plugin.Registry) *Compiler {
	c := &Compiler{
		logger:     logger,
		registry:   registry,
		validators: make([]Validator, 0),
	}

	// Register default validators
	c.AddValidator(&SnapshotValidator{})
	c.AddValidator(&RouteValidator{})
	c.AddValidator(&PluginValidator{registry: registry})

	return c
}

// AddValidator registers a new validation rule
func (c *Compiler) AddValidator(v Validator) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.validators = append(c.validators, v)
}

// Compile transforms a configuration snapshot into compiled configuration
func (c *Compiler) Compile(snapshot *provider.ConfigSnapshot) (*CompiledConfig, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Step 1: Validate snapshot
	if err := c.validate(snapshot); err != nil {
		c.logger.Error("Configuration validation failed", err)
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Step 2: Compile routes
	routes, err := c.compileRoutes(snapshot)
	if err != nil {
		return nil, fmt.Errorf("route compilation failed: %w", err)
	}

	// Step 3: Compile global config
	globals := c.compileGlobals(snapshot)

	// Step 4: Create compiled config
	compiled := &CompiledConfig{
		Version:    snapshot.Version,
		Routes:     routes,
		Globals:    globals,
		Metadata:   snapshot.Metadata,
		CompiledAt: snapshot.Timestamp,
	}

	c.logger.Info("Configuration compiled successfully",
		logging.String("version", compiled.Version),
		logging.Int("routes", len(compiled.Routes)),
	)

	return compiled, nil
}

// validate runs all registered validators
func (c *Compiler) validate(snapshot *provider.ConfigSnapshot) error {
	for _, validator := range c.validators {
		if err := validator.Validate(snapshot); err != nil {
			return err
		}
	}
	return nil
}

// compileRoutes transforms route configurations into execution pipelines
func (c *Compiler) compileRoutes(snapshot *provider.ConfigSnapshot) ([]*CompiledRoute, error) {
	var routes []*CompiledRoute

	for _, serviceConfig := range snapshot.Services {
		for _, service := range serviceConfig.Services {
			for _, route := range service.Routes {
				compiled, err := c.compileRoute(service, route)
				if err != nil {
					return nil, fmt.Errorf("failed to compile route %s: %w", route.Path, err)
				}
				routes = append(routes, compiled)
			}
		}
	}

	// Sort routes by priority (higher priority first)
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Priority > routes[j].Priority
	})

	return routes, nil
}

// compileRoute creates an execution pipeline for a single route
func (c *Compiler) compileRoute(service provider.Service, route provider.RouterConfig) (*CompiledRoute, error) {
	phaseMap := make(map[engine.Phase][]engine.PhasePlugin)

	// Get plugins for this route
	pluginNames := c.getRoutePlugins(route)

	for _, pluginName := range pluginNames {
		p, err := c.registry.Get(pluginName)
		if err != nil {
			return nil, fmt.Errorf("plugin not found: %s", pluginName)
		}

		phase := p.Phase()
		phaseMap[phase] = append(phaseMap[phase], p)
	}

	// Sort plugins within each phase by priority
	for phase := range phaseMap {
		sort.Slice(phaseMap[phase], func(i, j int) bool {
			return phaseMap[phase][i].Priority() > phaseMap[phase][j].Priority()
		})
	}

	// Build immutable pipeline in phase order
	pipeline := make(engine.CompiledPipeline, 0)
	for _, phase := range engine.PhaseOrder {
		if plugins, exists := phaseMap[phase]; exists && len(plugins) > 0 {
			pipeline = append(pipeline, engine.CompiledPhase{
				Phase:   phase,
				Plugins: plugins,
			})
		}
	}

	// Determine route ID
	routeID := route.Name
	if routeID == "" {
		routeID = fmt.Sprintf("%s:%s", service.Name, route.Path)
	}

	return &CompiledRoute{
		ID:       routeID,
		Path:     route.Path,
		Methods:  c.getMethods(route),
		Upstream: service.Host,
		Pipeline: pipeline,
		Metadata: map[string]interface{}{
			"service": service.Name,
			"group":   service.Group,
			"tags":    service.Tags,
		},
		Priority: route.Priority,
		Enabled:  !route.Enabled, // Note: config uses Enabled=false to disable
	}, nil
}

// getRoutePlugins extracts plugin names for a route
func (c *Compiler) getRoutePlugins(route provider.RouterConfig) []string {
	plugins := make([]string, 0)

	// Add global plugins first (from config)
	// These would come from snapshot.GlobalPlugins

	// Add route-specific plugins
	if route.AuthPlugin != "" {
		plugins = append(plugins, route.AuthPlugin)
	}

	// Add plugins based on route configuration
	if route.RequireAuth {
		plugins = append(plugins, "jwt") // or configured auth plugin
	}

	if route.RateLimit != nil && *route.RateLimit > 0 {
		plugins = append(plugins, "rate_limit")
	}

	// Add any explicitly configured plugins
	// This would need to be added to RouterConfig

	return plugins
}

// getMethods returns HTTP methods for a route
func (c *Compiler) getMethods(route provider.RouterConfig) []string {
	if route.Method != "" {
		return []string{route.Method}
	}
	if len(route.Methods) > 0 {
		return route.Methods
	}
	return []string{"GET"} // default
}

// compileGlobals extracts global configuration
func (c *Compiler) compileGlobals(snapshot *provider.ConfigSnapshot) *GlobalConfig {
	return &GlobalConfig{
		Port:            snapshot.Server.Port,
		DefaultTimeout:  30,
		MaxRequestSize:  10 * 1024 * 1024, // 10MB
		EnableMetrics:   true,
		EnableTracing:   false,
		SecurityHeaders: make(map[string]string),
	}
}

// Diff compares two compiled configurations and returns changes
func (c *Compiler) Diff(old, new *CompiledConfig) *ConfigDiff {
	diff := &ConfigDiff{
		Added:    make([]*CompiledRoute, 0),
		Removed:  make([]*CompiledRoute, 0),
		Modified: make([]*RouteDiff, 0),
	}

	oldRoutes := make(map[string]*CompiledRoute)
	for _, r := range old.Routes {
		oldRoutes[r.ID] = r
	}

	newRoutes := make(map[string]*CompiledRoute)
	for _, r := range new.Routes {
		newRoutes[r.ID] = r
	}

	// Find added and modified routes
	for id, newRoute := range newRoutes {
		if oldRoute, exists := oldRoutes[id]; exists {
			// Check if modified
			if !c.routesEqual(oldRoute, newRoute) {
				diff.Modified = append(diff.Modified, &RouteDiff{
					RouteID: id,
					Old:     oldRoute,
					New:     newRoute,
				})
			}
		} else {
			// New route
			diff.Added = append(diff.Added, newRoute)
		}
	}

	// Find removed routes
	for id, oldRoute := range oldRoutes {
		if _, exists := newRoutes[id]; !exists {
			diff.Removed = append(diff.Removed, oldRoute)
		}
	}

	return diff
}

// routesEqual compares two routes for equality
func (c *Compiler) routesEqual(r1, r2 *CompiledRoute) bool {
	// Simple comparison - in production, do deep comparison
	return r1.Path == r2.Path &&
		r1.Upstream == r2.Upstream &&
		r1.Priority == r2.Priority &&
		r1.Enabled == r2.Enabled
}

// ConfigDiff represents changes between two configurations
type ConfigDiff struct {
	Added    []*CompiledRoute
	Removed  []*CompiledRoute
	Modified []*RouteDiff
}

// RouteDiff represents changes to a single route
type RouteDiff struct {
	RouteID string
	Old     *CompiledRoute
	New     *CompiledRoute
}

// HasChanges returns true if there are any differences
func (d *ConfigDiff) HasChanges() bool {
	return len(d.Added) > 0 || len(d.Removed) > 0 || len(d.Modified) > 0
}

// Validators

// SnapshotValidator validates the basic structure of a config snapshot
type SnapshotValidator struct{}

func (v *SnapshotValidator) Validate(s *provider.ConfigSnapshot) error {
	if s == nil {
		return &ValidationError{Field: "snapshot", Message: "nil snapshot"}
	}

	if s.Version == "" {
		return &ValidationError{Field: "version", Message: "version is required"}
	}

	if len(s.Services) == 0 {
		return &ValidationError{Field: "services", Message: "at least one service is required"}
	}

	return nil
}

// RouteValidator validates route configurations
type RouteValidator struct{}

func (v *RouteValidator) Validate(s *provider.ConfigSnapshot) error {
	for _, serviceConfig := range s.Services {
		for _, service := range serviceConfig.Services {
			if service.Name == "" {
				return &ValidationError{Field: "service.name", Message: "service name is required"}
			}

			if service.Host == "" {
				return &ValidationError{Field: "service.host", Message: "service host is required"}
			}

			for _, route := range service.Routes {
				if route.Path == "" {
					return &ValidationError{
						Field:   fmt.Sprintf("service.%s.route.path", service.Name),
						Message: "route path is required",
					}
				}

				if route.Method == "" && len(route.Methods) == 0 {
					return &ValidationError{
						Field:   fmt.Sprintf("service.%s.route.%s.method", service.Name, route.Path),
						Message: "at least one HTTP method is required",
					}
				}
			}
		}
	}

	return nil
}

// PluginValidator validates plugin references
type PluginValidator struct {
	registry *plugin.Registry
}

func (v *PluginValidator) Validate(s *provider.ConfigSnapshot) error {
	// Collect all plugin references
	pluginRefs := make(map[string]bool)

	for _, serviceConfig := range s.Services {
		for _, service := range serviceConfig.Services {
			for _, route := range service.Routes {
				if route.AuthPlugin != "" {
					pluginRefs[route.AuthPlugin] = true
				}
			}
		}
	}

	// Check if all referenced plugins exist
	for pluginName := range pluginRefs {
		if _, err := v.registry.Get(pluginName); err != nil {
			return &ValidationError{
				Field:   "plugins",
				Message: fmt.Sprintf("plugin not found: %s", pluginName),
				Value:   pluginName,
			}
		}
	}

	return nil
}

// DryRun validates configuration without applying it
func (c *Compiler) DryRun(snapshot *provider.ConfigSnapshot) (*DryRunResult, error) {
	result := &DryRunResult{
		Valid:    true,
		Errors:   make([]*ValidationError, 0),
		Warnings: make([]string, 0),
	}

	// Run all validators
	for _, validator := range c.validators {
		if err := validator.Validate(snapshot); err != nil {
			result.Valid = false
			if validationErr, ok := err.(*ValidationError); ok {
				result.Errors = append(result.Errors, validationErr)
			} else {
				result.Errors = append(result.Errors, &ValidationError{
					Field:   "unknown",
					Message: err.Error(),
				})
			}
		}
	}

	// Try to compile
	if result.Valid {
		compiled, err := c.Compile(snapshot)
		if err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, &ValidationError{
				Field:   "compilation",
				Message: err.Error(),
			})
		} else {
			result.CompiledRoutes = len(compiled.Routes)
		}
	}

	return result, nil
}

// DryRunResult contains the result of a dry-run validation
type DryRunResult struct {
	Valid          bool
	Errors         []*ValidationError
	Warnings       []string
	CompiledRoutes int
}
