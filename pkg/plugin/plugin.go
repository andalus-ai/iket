package plugin

import (
	"fmt"
	"net/http"
	"sync"
)

// Plugin is the base interface all plugins must implement.
type Plugin interface {
	Name() string
	Initialize(config map[string]interface{}) error
}

// PluginType classifies legacy plugin registrations.
type PluginType string

const (
	AuthPlugin PluginType = "auth"
)

// LegacyFactory constructs plugins registered through the package-level API.
type LegacyFactory func(config map[string]interface{}) (Plugin, error)

var (
	legacyFactoryMu sync.RWMutex
	legacyFactories = make(map[string]LegacyFactory)
)

// RegisterFactory preserves the package-level registration API used by
// standalone authentication plugins. New code should prefer Registry.
func RegisterFactory(name string, factory LegacyFactory) error {
	if name == "" {
		return fmt.Errorf("plugin factory name is empty")
	}
	if factory == nil {
		return fmt.Errorf("plugin factory is nil")
	}
	legacyFactoryMu.Lock()
	defer legacyFactoryMu.Unlock()
	if _, exists := legacyFactories[name]; exists {
		return fmt.Errorf("plugin factory already registered: %s", name)
	}
	legacyFactories[name] = factory
	return nil
}

// NewRegisteredPlugin constructs a plugin registered through RegisterFactory.
func NewRegisteredPlugin(name string, config map[string]interface{}) (Plugin, error) {
	legacyFactoryMu.RLock()
	factory := legacyFactories[name]
	legacyFactoryMu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("plugin factory not found: %s", name)
	}
	return factory(config)
}

// MiddlewarePlugin is implemented by plugins that can wrap HTTP handlers.
type MiddlewarePlugin interface {
	Middleware(next http.Handler) http.Handler
}
