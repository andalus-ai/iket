package runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bhangun/iket/pkg/core/audit"
	"github.com/bhangun/iket/pkg/core/compiler"
	"github.com/bhangun/iket/pkg/core/provider"
	"github.com/bhangun/iket/pkg/logging"
)

// HotReloader manages zero-downtime configuration reloads
type HotReloader struct {
	// Current and staged configs
	current atomic.Value // *compiler.CompiledConfig
	staged  atomic.Value // *compiler.CompiledConfig

	// Version history for rollback
	history     []*compiler.CompiledConfig
	historySize int
	historyMu   sync.RWMutex

	// Lifecycle hooks
	beforeReload []ReloadHook
	afterReload  []ReloadHook
	onFailure    []FailureHook
	hooksMu      sync.RWMutex

	// Monitoring
	reloadCount  uint64
	failureCount uint64
	lastReload   time.Time
	lastReloadMu sync.RWMutex

	// Dependencies
	compiler *compiler.Compiler
	auditor  *audit.Emitter
	logger   *logging.Logger
}

// ReloadHook is called before/after reload
type ReloadHook func(old, new *compiler.CompiledConfig) error

// FailureHook is called when reload fails
type FailureHook func(err error, staged *compiler.CompiledConfig)

// NewHotReloader creates a new hot reloader
func NewHotReloader(
	comp *compiler.Compiler,
	auditor *audit.Emitter,
	logger *logging.Logger,
) *HotReloader {
	return &HotReloader{
		compiler:     comp,
		auditor:      auditor,
		logger:       logger,
		historySize:  10,
		history:      make([]*compiler.CompiledConfig, 0, 10),
		beforeReload: make([]ReloadHook, 0),
		afterReload:  make([]ReloadHook, 0),
		onFailure:    make([]FailureHook, 0),
	}
}

// GetCurrent returns the current active configuration
func (r *HotReloader) GetCurrent() *compiler.CompiledConfig {
	if cfg := r.current.Load(); cfg != nil {
		return cfg.(*compiler.CompiledConfig)
	}
	return nil
}

// SetInitial sets the initial configuration
func (r *HotReloader) SetInitial(cfg *compiler.CompiledConfig) {
	r.current.Store(cfg)
	r.addToHistory(cfg)

	r.logger.Info("Initial configuration set",
		logging.String("version", cfg.Version),
		logging.Int("routes", len(cfg.Routes)),
	)
}

// Reload performs a zero-downtime configuration reload
func (r *HotReloader) Reload(ctx context.Context, snapshot *provider.ConfigSnapshot) error {
	startTime := time.Now()

	r.logger.Info("Starting configuration reload",
		logging.String("snapshot_version", snapshot.Version),
	)

	// Step 1: Validate snapshot
	if err := snapshot.Validate(); err != nil {
		r.recordFailure()
		return fmt.Errorf("snapshot validation failed: %w", err)
	}

	// Step 2: Compile new configuration
	compiled, err := r.compiler.Compile(snapshot)
	if err != nil {
		r.recordFailure()
		r.emitAuditEvent(audit.EventConfigValidated, false, err.Error())
		return fmt.Errorf("compilation failed: %w", err)
	}

	r.logger.Info("Configuration compiled successfully",
		logging.String("version", compiled.Version),
		logging.Int("routes", len(compiled.Routes)),
		logging.Duration("compile_time", time.Since(startTime)),
	)

	// Step 3: Stage the new configuration
	r.staged.Store(compiled)

	// Step 4: Run before-reload hooks
	old := r.GetCurrent()
	if err := r.runBeforeHooks(old, compiled); err != nil {
		r.recordFailure()
		r.emitAuditEvent(audit.EventConfigUpdated, false, err.Error())
		return fmt.Errorf("before-reload hook failed: %w", err)
	}

	// Step 5: Atomic swap
	r.swapConfig(compiled)

	// Step 6: Run after-reload hooks
	if err := r.runAfterHooks(old, compiled); err != nil {
		// Log but don't fail - config is already swapped
		r.logger.Error("After-reload hook failed", err)
	}

	// Step 7: Update metrics and history
	r.recordSuccess()
	r.addToHistory(compiled)

	duration := time.Since(startTime)

	r.logger.Info("Configuration reload completed",
		logging.String("version", compiled.Version),
		logging.Duration("total_time", duration),
	)

	// Emit audit event
	r.emitAuditEvent(audit.EventConfigUpdated, true, "Configuration reloaded successfully")

	return nil
}

// swapConfig performs the atomic configuration swap
func (r *HotReloader) swapConfig(new *compiler.CompiledConfig) {
	// Atomic swap ensures readers see either old OR new config
	// Never a partial or inconsistent state
	r.current.Store(new)

	r.lastReloadMu.Lock()
	r.lastReload = time.Now()
	r.lastReloadMu.Unlock()

	r.logger.Info("Configuration swapped atomically",
		logging.String("version", new.Version),
	)
}

// Rollback reverts to a previous configuration version
func (r *HotReloader) Rollback(version string) error {
	r.historyMu.RLock()
	var target *compiler.CompiledConfig
	for _, cfg := range r.history {
		if cfg.Version == version {
			target = cfg
			break
		}
	}
	r.historyMu.RUnlock()

	if target == nil {
		return fmt.Errorf("version not found in history: %s", version)
	}

	r.logger.Info("Rolling back configuration",
		logging.String("target_version", version),
	)

	// Perform rollback (similar to reload but with historical config)
	old := r.GetCurrent()

	// Run before hooks
	if err := r.runBeforeHooks(old, target); err != nil {
		return fmt.Errorf("before-rollback hook failed: %w", err)
	}

	// Swap
	r.swapConfig(target)

	// Run after hooks
	if err := r.runAfterHooks(old, target); err != nil {
		r.logger.Error("After-rollback hook failed", err)
	}

	r.logger.Info("Configuration rolled back",
		logging.String("version", version),
	)

	// Emit audit event
	r.emitAuditEvent(audit.EventConfigRolledBack, true,
		fmt.Sprintf("Rolled back to version %s", version))

	return nil
}

// DryRun validates a snapshot without applying it
func (r *HotReloader) DryRun(snapshot *provider.ConfigSnapshot) (*compiler.DryRunResult, error) {
	r.logger.Info("Starting dry-run validation",
		logging.String("snapshot_version", snapshot.Version),
	)

	result, err := r.compiler.DryRun(snapshot)
	if err != nil {
		return nil, fmt.Errorf("dry-run failed: %w", err)
	}

	if !result.Valid {
		r.logger.Warn("Dry-run validation failed",
			logging.Int("errors", len(result.Errors)),
		)
	} else {
		r.logger.Info("Dry-run validation passed",
			logging.Int("routes", result.CompiledRoutes),
		)
	}

	return result, nil
}

// GetHistory returns the configuration version history
func (r *HotReloader) GetHistory() []string {
	r.historyMu.RLock()
	defer r.historyMu.RUnlock()

	versions := make([]string, len(r.history))
	for i, cfg := range r.history {
		versions[i] = cfg.Version
	}

	return versions
}

// Diff compares current configuration with a snapshot
func (r *HotReloader) Diff(snapshot *provider.ConfigSnapshot) (*compiler.ConfigDiff, error) {
	// Compile the new snapshot
	newCompiled, err := r.compiler.Compile(snapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to compile snapshot: %w", err)
	}

	// Get current config
	current := r.GetCurrent()
	if current == nil {
		return nil, fmt.Errorf("no current configuration")
	}

	// Generate diff
	diff := r.compiler.Diff(current, newCompiled)

	return diff, nil
}

// Hooks management

func (r *HotReloader) AddBeforeReloadHook(hook ReloadHook) {
	r.hooksMu.Lock()
	defer r.hooksMu.Unlock()
	r.beforeReload = append(r.beforeReload, hook)
}

func (r *HotReloader) AddAfterReloadHook(hook ReloadHook) {
	r.hooksMu.Lock()
	defer r.hooksMu.Unlock()
	r.afterReload = append(r.afterReload, hook)
}

func (r *HotReloader) AddFailureHook(hook FailureHook) {
	r.hooksMu.Lock()
	defer r.hooksMu.Unlock()
	r.onFailure = append(r.onFailure, hook)
}

func (r *HotReloader) runBeforeHooks(old, new *compiler.CompiledConfig) error {
	r.hooksMu.RLock()
	hooks := r.beforeReload
	r.hooksMu.RUnlock()

	for _, hook := range hooks {
		if err := hook(old, new); err != nil {
			return err
		}
	}

	return nil
}

func (r *HotReloader) runAfterHooks(old, new *compiler.CompiledConfig) error {
	r.hooksMu.RLock()
	hooks := r.afterReload
	r.hooksMu.RUnlock()

	for _, hook := range hooks {
		if err := hook(old, new); err != nil {
			return err
		}
	}

	return nil
}

func (r *HotReloader) runFailureHooks(err error, staged *compiler.CompiledConfig) {
	r.hooksMu.RLock()
	hooks := r.onFailure
	r.hooksMu.RUnlock()

	for _, hook := range hooks {
		hook(err, staged)
	}
}

// History management

func (r *HotReloader) addToHistory(cfg *compiler.CompiledConfig) {
	r.historyMu.Lock()
	defer r.historyMu.Unlock()

	// Add to history
	r.history = append(r.history, cfg)

	// Trim if exceeds size
	if len(r.history) > r.historySize {
		r.history = r.history[1:]
	}
}

// Metrics

func (r *HotReloader) recordSuccess() {
	atomic.AddUint64(&r.reloadCount, 1)
}

func (r *HotReloader) recordFailure() {
	atomic.AddUint64(&r.failureCount, 1)
}

func (r *HotReloader) GetStats() *ReloadStats {
	r.lastReloadMu.RLock()
	lastReload := r.lastReload
	r.lastReloadMu.RUnlock()

	return &ReloadStats{
		TotalReloads:   atomic.LoadUint64(&r.reloadCount),
		FailedReloads:  atomic.LoadUint64(&r.failureCount),
		LastReload:     lastReload,
		HistorySize:    len(r.GetHistory()),
		CurrentVersion: r.getCurrentVersion(),
	}
}

type ReloadStats struct {
	TotalReloads   uint64    `json:"total_reloads"`
	FailedReloads  uint64    `json:"failed_reloads"`
	LastReload     time.Time `json:"last_reload"`
	HistorySize    int       `json:"history_size"`
	CurrentVersion string    `json:"current_version"`
}

func (r *HotReloader) getCurrentVersion() string {
	if cfg := r.GetCurrent(); cfg != nil {
		return cfg.Version
	}
	return ""
}

// Audit

func (r *HotReloader) emitAuditEvent(eventType audit.EventType, success bool, message string) {
	if r.auditor == nil {
		return
	}

	event := &audit.Event{
		Type:     eventType,
		Severity: audit.SeverityInfo,
		Category: "config",
		Message:  message,
		Data: map[string]interface{}{
			"success": success,
		},
		Actor: &audit.Actor{
			Type: "system",
			ID:   "hot_reloader",
		},
	}

	r.auditor.Emit(event)
}
