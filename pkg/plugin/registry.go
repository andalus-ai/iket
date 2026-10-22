package plugin

import (
	"fmt"
	"sync"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
	"github.com/bhangun/iket/pkg/plugin/builtin/auth"
	"github.com/bhangun/iket/pkg/plugin/builtin/policy"
	"github.com/bhangun/iket/pkg/plugin/builtin/traffic"
	"github.com/bhangun/iket/pkg/plugin/builtin/transform"
	"github.com/bhangun/iket/pkg/plugin/builtin/upstream"
	"github.com/bhangun/iket/pkg/plugin/builtin/validation"
	"github.com/bhangun/iket/plugins/graphql"
	"github.com/bhangun/iket/plugins/metrics"
	"github.com/bhangun/iket/plugins/openapi"
)

// Factory creates a plugin instance from configuration
type Factory func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error)

// Registry manages plugin factories and instances
type Registry struct {
	factories map[string]Factory
	plugins   map[string]engine.PhasePlugin
	logger    *logging.Logger
	mu        sync.RWMutex
}

// NewRegistry creates a new plugin registry
func NewRegistry(logger *logging.Logger) *Registry {
	return &Registry{
		factories: make(map[string]Factory),
		plugins:   make(map[string]engine.PhasePlugin),
		logger:    logger,
	}
}

// RegisterFactory registers a plugin factory
func (r *Registry) RegisterFactory(name string, factory Factory) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("factory already registered: %s", name)
	}

	r.factories[name] = factory

	r.logger.Debug("Plugin factory registered",
		logging.String("plugin", name),
	)

	return nil
}

// Get retrieves or creates a plugin instance
func (r *Registry) Get(name string) (engine.PhasePlugin, error) {
	r.mu.RLock()

	// Check if plugin already exists
	if plugin, exists := r.plugins[name]; exists {
		r.mu.RUnlock()
		return plugin, nil
	}

	// Check if factory exists
	factory, exists := r.factories[name]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("plugin not found: %s", name)
	}

	// Create plugin instance
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if plugin, exists := r.plugins[name]; exists {
		return plugin, nil
	}

	// Create new instance
	// Note: config usually comes from higher level, passing empty here for factory registration check
	// The actual initialization happens via InitializePlugin
	plugin, err := factory(r.logger, make(map[string]interface{}))
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin %s: %w", name, err)
	}

	r.plugins[name] = plugin

	r.logger.Info("Plugin instance created",
		logging.String("plugin", name),
	)

	return plugin, nil
}

// List returns all registered plugin names
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}

	return names
}

// InitializePlugin initializes a plugin with configuration
func (r *Registry) InitializePlugin(name string, config map[string]interface{}) error {
	plugin, err := r.Get(name)
	if err != nil {
		return err
	}

	if initializable, ok := plugin.(interface {
		Initialize(map[string]interface{}) error
	}); ok {
		return initializable.Initialize(config)
	}

	return nil
}

// RegisterAllBuiltins registers all built-in plugins
func (r *Registry) RegisterAllBuiltins() error {
	// JWT Authentication
	if err := r.RegisterFactory("jwt", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := auth.NewJWTPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// Rate Limiting
	if err := r.RegisterFactory("rate_limit", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := traffic.NewRateLimitPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// OPA Policy
	if err := r.RegisterFactory("opa_policy", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := policy.NewOPAPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// Circuit Breaker
	if err := r.RegisterFactory("circuit_breaker", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := traffic.NewCircuitBreakerPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// Upstream Proxy
	if err := r.RegisterFactory("upstream_proxy", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := upstream.NewUpstreamProxyPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// IP Whitelist
	if err := r.RegisterFactory("ip_whitelist", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := traffic.NewIPWhitelistPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// CORS
	if err := r.RegisterFactory("cors", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := transform.NewCORSPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// Validation
	if err := r.RegisterFactory("validation", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := validation.NewValidationPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// Metrics
	if err := r.RegisterFactory("prometheus", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := metrics.NewPrometheusPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// OpenAPI
	if err := r.RegisterFactory("openapi", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := openapi.NewOpenAPIPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// mTLS
	if err := r.RegisterFactory("mtls", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := auth.NewMTLSPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// OAuth2
	if err := r.RegisterFactory("oauth2", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := auth.NewOAuth2Plugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// TLS
	if err := r.RegisterFactory("tls", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := auth.NewTLSPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// WebSocket
	if err := r.RegisterFactory("websocket", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := upstream.NewWebSocketPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	// GraphQL
	if err := r.RegisterFactory("graphql", func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error) {
		plugin := graphql.NewGraphQLPlugin(logger)
		if err := plugin.Initialize(config); err != nil {
			return nil, err
		}
		return plugin, nil
	}); err != nil {
		return err
	}

	r.logger.Info("All built-in plugins registered")

	return nil
}

// Global registry instance (optional convenience)
var DefaultRegistry *Registry

// InitializeDefaultRegistry creates and initializes the default registry
func InitializeDefaultRegistry(logger *logging.Logger) error {
	DefaultRegistry = NewRegistry(logger)
	return DefaultRegistry.RegisterAllBuiltins()
}
