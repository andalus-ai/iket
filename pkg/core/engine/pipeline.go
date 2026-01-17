package engine

// CompiledPhase represents a phase with its ordered plugins
type CompiledPhase struct {
	Phase   Phase
	Plugins []PhasePlugin
}

// CompiledPipeline is an ordered, immutable sequence of phases
type CompiledPipeline []CompiledPhase
