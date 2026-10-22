package compiler

import (
	"fmt"
	"sort"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
	"github.com/bhangun/iket/pkg/plugin"
)

// PipelineBuilder builds executable pipelines from configuration
type PipelineBuilder struct {
	registry *plugin.Registry
	logger   *logging.Logger
}

// NewPipelineBuilder creates a new pipeline builder
func NewPipelineBuilder(registry *plugin.Registry, logger *logging.Logger) *PipelineBuilder {
	return &PipelineBuilder{
		registry: registry,
		logger:   logger,
	}
}

// BuildPipeline constructs an executable pipeline for a route
func (b *PipelineBuilder) BuildPipeline(route *RouteConfig, globalPlugins map[string]PluginConfig) (engine.CompiledPipeline, error) {
	// Collect all plugins for this route
	pluginList, err := b.collectPlugins(route, globalPlugins)
	if err != nil {
		return nil, fmt.Errorf("failed to collect plugins: %w", err)
	}

	// Resolve plugin instances
	plugins, err := b.resolvePlugins(pluginList)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve plugins: %w", err)
	}

	// Group by phase
	phaseMap := b.groupByPhase(plugins)

	// Sort within each phase
	b.sortByPriority(phaseMap)

	// Build compiled pipeline
	pipeline := b.buildCompiledPipeline(phaseMap)

	b.logger.Debug("Pipeline built",
		logging.String("route", route.Path),
		logging.Int("phases", len(pipeline)),
		logging.Int("total_plugins", b.countPlugins(pipeline)),
	)

	return pipeline, nil
}

// collectPlugins gathers all plugins that should run for a route
func (b *PipelineBuilder) collectPlugins(route *RouteConfig, globalPlugins map[string]PluginConfig) ([]PluginRef, error) {
	refs := make([]PluginRef, 0)

	// Add global plugins (if enabled)
	for name, cfg := range globalPlugins {
		if !cfg.Enabled {
			continue
		}

		refs = append(refs, PluginRef{
			Name:     name,
			Priority: cfg.Priority,
			Config:   cfg.Config,
			Scope:    ScopeGlobal,
		})
	}

	// Add route-specific plugins
	for _, name := range route.Plugins {
		refs = append(refs, PluginRef{
			Name:     name,
			Priority: 0, // Will use plugin's default priority
			Config:   make(map[string]interface{}),
			Scope:    ScopeRoute,
		})
	}

	// Remove duplicates (route-specific overrides global)
	return b.deduplicatePlugins(refs), nil
}

// resolvePlugins converts plugin references to actual plugin instances
func (b *PipelineBuilder) resolvePlugins(refs []PluginRef) ([]engine.PhasePlugin, error) {
	plugins := make([]engine.PhasePlugin, 0, len(refs))

	for _, ref := range refs {
		p, err := b.registry.Get(ref.Name)
		if err != nil {
			return nil, fmt.Errorf("plugin not found: %s", ref.Name)
		}

		// Type assertion to PhasePlugin
		phasePlugin, ok := p.(engine.PhasePlugin)
		if !ok {
			return nil, fmt.Errorf("plugin %s does not implement PhasePlugin", ref.Name)
		}

		// Initialize if not already done
		if len(ref.Config) > 0 {
			if initializable, ok := p.(interface {
				Initialize(map[string]interface{}) error
			}); ok {
				if err := initializable.Initialize(ref.Config); err != nil {
					return nil, fmt.Errorf("failed to initialize plugin %s: %w", ref.Name, err)
				}
			}
		}

		plugins = append(plugins, phasePlugin)
	}

	return plugins, nil
}

// groupByPhase organizes plugins by their execution phase
func (b *PipelineBuilder) groupByPhase(plugins []engine.PhasePlugin) map[engine.Phase][]engine.PhasePlugin {
	phaseMap := make(map[engine.Phase][]engine.PhasePlugin)

	for _, plugin := range plugins {
		phase := plugin.Phase()
		phaseMap[phase] = append(phaseMap[phase], plugin)
	}

	return phaseMap
}

// sortByPriority sorts plugins within each phase by priority
func (b *PipelineBuilder) sortByPriority(phaseMap map[engine.Phase][]engine.PhasePlugin) {
	for phase := range phaseMap {
		sort.Slice(phaseMap[phase], func(i, j int) bool {
			return phaseMap[phase][i].Priority() > phaseMap[phase][j].Priority()
		})
	}
}

// buildCompiledPipeline creates an ordered pipeline from phase map
func (b *PipelineBuilder) buildCompiledPipeline(phaseMap map[engine.Phase][]engine.PhasePlugin) engine.CompiledPipeline {
	pipeline := make(engine.CompiledPipeline, 0)

	// Build pipeline in deterministic phase order
	for _, phase := range engine.PhaseOrder {
		if plugins, exists := phaseMap[phase]; exists && len(plugins) > 0 {
			pipeline = append(pipeline, engine.CompiledPhase{
				Phase:   phase,
				Plugins: plugins,
			})
		}
	}

	return pipeline
}

// deduplicatePlugins removes duplicate plugin references (route overrides global)
func (b *PipelineBuilder) deduplicatePlugins(refs []PluginRef) []PluginRef {
	seen := make(map[string]*PluginRef)
	result := make([]PluginRef, 0)

	// Process in reverse to prioritize route-specific over global
	for i := len(refs) - 1; i >= 0; i-- {
		ref := refs[i]
		if existing, exists := seen[ref.Name]; !exists {
			seen[ref.Name] = &ref
		} else {
			// Route scope overrides global scope
			if ref.Scope == ScopeRoute && existing.Scope == ScopeGlobal {
				seen[ref.Name] = &ref
			}
		}
	}

	// Collect unique refs
	for _, ref := range seen {
		result = append(result, *ref)
	}

	return result
}

// countPlugins counts total plugins in a pipeline
func (b *PipelineBuilder) countPlugins(pipeline engine.CompiledPipeline) int {
	count := 0
	for _, phase := range pipeline {
		count += len(phase.Plugins)
	}
	return count
}

// PluginRef represents a reference to a plugin
type PluginRef struct {
	Name     string
	Priority int
	Config   map[string]interface{}
	Scope    PluginScope
}

// PluginScope indicates where the plugin was configured
type PluginScope string

const (
	ScopeGlobal PluginScope = "global"
	ScopeRoute  PluginScope = "route"
)

// ValidatePipeline checks if a pipeline is valid
func (b *PipelineBuilder) ValidatePipeline(pipeline engine.CompiledPipeline) error {
	if len(pipeline) == 0 {
		return fmt.Errorf("pipeline is empty")
	}

	// Check phase order
	lastPhaseIndex := -1
	for _, compiledPhase := range pipeline {
		currentPhaseIndex := b.getPhaseIndex(compiledPhase.Phase)
		if currentPhaseIndex <= lastPhaseIndex {
			return fmt.Errorf("phases out of order: %s after %s",
				compiledPhase.Phase,
				engine.PhaseOrder[lastPhaseIndex])
		}
		lastPhaseIndex = currentPhaseIndex

		// Validate plugins in phase
		if len(compiledPhase.Plugins) == 0 {
			return fmt.Errorf("phase %s has no plugins", compiledPhase.Phase)
		}

		for _, plugin := range compiledPhase.Plugins {
			if plugin.Phase() != compiledPhase.Phase {
				return fmt.Errorf("plugin %s in wrong phase: expected %s, got %s",
					plugin.Name(),
					compiledPhase.Phase,
					plugin.Phase())
			}
		}
	}

	return nil
}

// getPhaseIndex returns the index of a phase in the order
func (b *PipelineBuilder) getPhaseIndex(phase engine.Phase) int {
	for i, p := range engine.PhaseOrder {
		if p == phase {
			return i
		}
	}
	return -1
}

// PipelineStats provides statistics about a pipeline
type PipelineStats struct {
	TotalPhases    int                  `json:"total_phases"`
	TotalPlugins   int                  `json:"total_plugins"`
	PhaseBreakdown map[engine.Phase]int `json:"phase_breakdown"`
	PluginList     []string             `json:"plugin_list"`
}

// GetPipelineStats computes statistics for a pipeline
func GetPipelineStats(pipeline engine.CompiledPipeline) *PipelineStats {
	stats := &PipelineStats{
		TotalPhases:    len(pipeline),
		TotalPlugins:   0,
		PhaseBreakdown: make(map[engine.Phase]int),
		PluginList:     make([]string, 0),
	}

	for _, phase := range pipeline {
		count := len(phase.Plugins)
		stats.TotalPlugins += count
		stats.PhaseBreakdown[phase.Phase] = count

		for _, plugin := range phase.Plugins {
			stats.PluginList = append(stats.PluginList, plugin.Name())
		}
	}

	return stats
}

// OptimizePipeline performs optimizations on the pipeline
func (b *PipelineBuilder) OptimizePipeline(pipeline engine.CompiledPipeline) engine.CompiledPipeline {
	// For now, just remove empty phases
	// Future optimizations:
	// - Merge compatible plugins
	// - Parallelize independent plugins
	// - Remove no-op plugins

	optimized := make(engine.CompiledPipeline, 0, len(pipeline))

	for _, phase := range pipeline {
		if len(phase.Plugins) > 0 {
			optimized = append(optimized, phase)
		}
	}

	return optimized
}

// ClonePipeline creates a deep copy of a pipeline
func ClonePipeline(pipeline engine.CompiledPipeline) engine.CompiledPipeline {
	clone := make(engine.CompiledPipeline, len(pipeline))

	for i, phase := range pipeline {
		clone[i] = engine.CompiledPhase{
			Phase:   phase.Phase,
			Plugins: append([]engine.PhasePlugin{}, phase.Plugins...),
		}
	}

	return clone
}
