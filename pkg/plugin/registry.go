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

// Factory creates a plugin instance from configuration.
type Factory func(logger *logging.Logger, config map[string]interface{}) (engine.PhasePlugin, error)

// Registry manages plugin factories and instances.
type Registry struct {
	factories               map[string]Factory
	plugins                 map[string]engine.PhasePlugin
	clientUsageObserverWires map[string]map[string]struct{}
	logger                  *logging.Logger
	mu                      sync.RWMutex
}

// NewRegistry creates a new plugin registry.
func NewRegistry(logger *logging.Logger) *Registry {
	return &Registry{
		factories: make(map[string]Factory),
		plugins:   make(map[string]engine.PhasePlugin),
		logger:    logger,
	}
}
