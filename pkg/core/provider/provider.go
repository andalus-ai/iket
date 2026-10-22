package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/config"
	"github.com/bhangun/iket/pkg/logging"
)

// ProviderRole defines the role of a configuration provider
type ProviderRole string

const (
	RoleBootstrap ProviderRole = "bootstrap" // Initial config at startup
	RoleRuntime   ProviderRole = "runtime"   // Dynamic runtime config
	RoleFallback  ProviderRole = "fallback"  // Backup when runtime fails
)

// ConfigSnapshot represents an immutable configuration snapshot
type ConfigSnapshot struct {
	Version   string
	Timestamp int64
	Source    string
	Role      ProviderRole

	// Configuration data
	Services []config.ServiceConfig
	Plugins  map[string]map[string]interface{}
	Security config.SecurityConfig
	Server   config.ServerConfig

	// Metadata
	Metadata map[string]interface{}
}

// Validate performs basic validation on the snapshot
func (s *ConfigSnapshot) Validate() error {
	if s.Version == "" {
		return fmt.Errorf("snapshot version is required")
	}

	if len(s.Services) == 0 {
		return fmt.Errorf("at least one service is required")
	}

	return nil
}

// Provider defines the interface for configuration sources
type Provider interface {
	// Name returns the provider name
	Name() string

	// Role returns the provider's role
	Role() ProviderRole

	// Load retrieves the current configuration
	Load(ctx context.Context) (*ConfigSnapshot, error)

	// Watch monitors for configuration changes
	Watch(ctx context.Context, callback func(*ConfigSnapshot) error) error

	// Close releases resources
	Close() error
}

// Service represents a backend service configuration
type Service = config.Service

// RouterConfig represents a route configuration
type RouterConfig = config.RouterConfig

// ConfigManager orchestrates multiple providers and produces unified snapshots
type ConfigManager struct {
	logger    *logging.Logger
	providers map[string]Provider

	// Current active snapshot
	current   *ConfigSnapshot
	currentMu sync.RWMutex

	// Change notifications
	onChange []func(*ConfigSnapshot) error
	changeMu sync.RWMutex

	// Merge strategy
	strategy MergeStrategy
}

// MergeStrategy defines how to combine configurations from multiple providers
type MergeStrategy interface {
	Merge(snapshots []*ConfigSnapshot) (*ConfigSnapshot, error)
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(logger *logging.Logger, strategy MergeStrategy) *ConfigManager {
	if strategy == nil {
		strategy = &OverlayMergeStrategy{}
	}

	return &ConfigManager{
		logger:    logger,
		providers: make(map[string]Provider),
		onChange:  make([]func(*ConfigSnapshot) error, 0),
		strategy:  strategy,
	}
}

// RegisterProvider adds a new configuration provider
func (m *ConfigManager) RegisterProvider(p Provider) error {
	m.currentMu.Lock()
	defer m.currentMu.Unlock()

	name := p.Name()
	if _, exists := m.providers[name]; exists {
		return fmt.Errorf("provider %s already registered", name)
	}

	m.providers[name] = p
	m.logger.Info("Configuration provider registered",
		logging.String("provider", name),
		logging.String("role", string(p.Role())),
	)

	return nil
}

// Load retrieves and merges configuration from all providers
func (m *ConfigManager) Load(ctx context.Context) (*ConfigSnapshot, error) {
	snapshots := make([]*ConfigSnapshot, 0)

	// Load from all providers
	for name, provider := range m.providers {
		snapshot, err := provider.Load(ctx)
		if err != nil {
			// If runtime provider fails, log but continue with fallback
			if provider.Role() == RoleRuntime {
				m.logger.Warn("Runtime provider failed, using fallback",
					logging.String("provider", name),
					logging.Error(err),
				)
				continue
			}

			// Bootstrap and fallback failures are critical
			return nil, fmt.Errorf("provider %s load failed: %w", name, err)
		}

		snapshots = append(snapshots, snapshot)
	}

	// Merge snapshots using strategy
	merged, err := m.strategy.Merge(snapshots)
	if err != nil {
		return nil, fmt.Errorf("merge failed: %w", err)
	}

	// Update current snapshot
	m.currentMu.Lock()
	m.current = merged
	m.currentMu.Unlock()

	m.logger.Info("Configuration loaded",
		logging.String("version", merged.Version),
		logging.Int("providers", len(snapshots)),
	)

	return merged, nil
}

// Watch monitors all providers for changes
func (m *ConfigManager) Watch(ctx context.Context) error {
	for name, provider := range m.providers {
		// Only watch runtime providers
		if provider.Role() != RoleRuntime {
			continue
		}

		go func(name string, p Provider) {
			err := p.Watch(ctx, func(snapshot *ConfigSnapshot) error {
				return m.handleChange(ctx, snapshot)
			})

			if err != nil {
				m.logger.Error("Provider watch failed",
					err,
					logging.String("provider", name),
				)
			}
		}(name, provider)
	}

	return nil
}

// handleChange processes a configuration change
func (m *ConfigManager) handleChange(ctx context.Context, changed *ConfigSnapshot) error {
	m.logger.Info("Configuration change detected",
		logging.String("version", changed.Version),
		logging.String("source", changed.Source),
	)

	// Reload to get fresh merged snapshot
	merged, err := m.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to reload after change: %w", err)
	}

	// Notify all change handlers
	m.changeMu.RLock()
	handlers := make([]func(*ConfigSnapshot) error, len(m.onChange))
	copy(handlers, m.onChange)
	m.changeMu.RUnlock()

	for _, handler := range handlers {
		if err := handler(merged); err != nil {
			m.logger.Error("Change handler failed", err)
			// Continue with other handlers
		}
	}

	return nil
}

// OnChange registers a callback for configuration changes
func (m *ConfigManager) OnChange(callback func(*ConfigSnapshot) error) {
	m.changeMu.Lock()
	defer m.changeMu.Unlock()
	m.onChange = append(m.onChange, callback)
}

// Current returns the current active configuration snapshot
func (m *ConfigManager) Current() *ConfigSnapshot {
	m.currentMu.RLock()
	defer m.currentMu.RUnlock()
	return m.current
}

// Close releases all provider resources
func (m *ConfigManager) Close() error {
	for name, provider := range m.providers {
		if err := provider.Close(); err != nil {
			m.logger.Error("Provider close failed",
				err,
				logging.String("provider", name),
			)
		}
	}
	return nil
}

// OverlayMergeStrategy implements overlay merge (runtime overrides bootstrap)
type OverlayMergeStrategy struct{}

func (s *OverlayMergeStrategy) Merge(snapshots []*ConfigSnapshot) (*ConfigSnapshot, error) {
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("no snapshots to merge")
	}

	// Sort by role priority: bootstrap < fallback < runtime
	rolePriority := map[ProviderRole]int{
		RoleBootstrap: 1,
		RoleFallback:  2,
		RoleRuntime:   3,
	}

	// Find highest priority snapshot
	var base *ConfigSnapshot
	for _, snap := range snapshots {
		if base == nil || rolePriority[snap.Role] > rolePriority[base.Role] {
			base = snap
		}
	}

	// Create merged snapshot
	merged := &ConfigSnapshot{
		Version:   base.Version,
		Timestamp: time.Now().Unix(),
		Source:    "merged",
		Role:      RoleRuntime,
		Services:  base.Services,
		Plugins:   make(map[string]map[string]interface{}),
		Security:  base.Security,
		Server:    base.Server,
		Metadata:  make(map[string]interface{}),
	}

	// Merge plugins from all snapshots (runtime wins)
	for _, snap := range snapshots {
		for pluginName, pluginConfig := range snap.Plugins {
			merged.Plugins[pluginName] = deepCopyMap(pluginConfig)
		}
	}

	return merged, nil
}

// deepCopyMap creates a deep copy of a map
func deepCopyMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{})
	for k, v := range src {
		switch val := v.(type) {
		case map[string]interface{}:
			dst[k] = deepCopyMap(val)
		case []interface{}:
			dst[k] = deepCopySlice(val)
		default:
			dst[k] = val
		}
	}
	return dst
}

// deepCopySlice creates a deep copy of a slice
func deepCopySlice(src []interface{}) []interface{} {
	dst := make([]interface{}, len(src))
	for i, v := range src {
		switch val := v.(type) {
		case map[string]interface{}:
			dst[i] = deepCopyMap(val)
		case []interface{}:
			dst[i] = deepCopySlice(val)
		default:
			dst[i] = val
		}
	}
	return dst
}

// FileProvider implements Provider for file-based configuration
type FileProvider struct {
	configPath string
	logger     *logging.Logger
	role       ProviderRole
}

// NewFileProvider creates a file-based configuration provider
func NewFileProvider(configPath string, role ProviderRole, logger *logging.Logger) *FileProvider {
	return &FileProvider{
		configPath: configPath,
		logger:     logger,
		role:       role,
	}
}

func (p *FileProvider) Name() string {
	return "file"
}

func (p *FileProvider) Role() ProviderRole {
	return p.role
}

func (p *FileProvider) Load(ctx context.Context) (*ConfigSnapshot, error) {
	// Load config using existing config.LoadConfig
	cfg, err := config.LoadFromFile(p.configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load file config: %w", err)
	}

	return &ConfigSnapshot{
		Version:   "file-" + time.Now().Format("20060102150405"),
		Timestamp: time.Now().Unix(),
		Source:    p.configPath,
		Role:      p.role,
		Services:  cfg.Services,
		Plugins:   cfg.Plugins,
		Security:  cfg.Security,
		Server:    cfg.Server,
		Metadata:  make(map[string]interface{}),
	}, nil
}

func (p *FileProvider) Watch(ctx context.Context, callback func(*ConfigSnapshot) error) error {
	// File watching implementation
	// For now, just poll every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Check file modification time
			// If changed, load and call callback
			// This is simplified - use fsnotify in production
		}
	}
}

func (p *FileProvider) Close() error {
	return nil
}

// DatabaseProvider implements Provider for database-backed configuration
type DatabaseProvider struct {
	db     DatabaseClient
	logger *logging.Logger
	role   ProviderRole
}

// DatabaseClient defines the interface for database operations
type DatabaseClient interface {
	LoadConfig(ctx context.Context) ([]byte, error)
	WatchConfig(ctx context.Context, callback func([]byte) error) error
	Close() error
}

// NewDatabaseProvider creates a database-backed configuration provider
func NewDatabaseProvider(db DatabaseClient, logger *logging.Logger) *DatabaseProvider {
	return &DatabaseProvider{
		db:     db,
		logger: logger,
		role:   RoleRuntime,
	}
}

func (p *DatabaseProvider) Name() string {
	return "database"
}

func (p *DatabaseProvider) Role() ProviderRole {
	return p.role
}

func (p *DatabaseProvider) Load(ctx context.Context) (*ConfigSnapshot, error) {
	data, err := p.db.LoadConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("database load failed: %w", err)
	}

	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &ConfigSnapshot{
		Version:   "db-" + time.Now().Format("20060102150405"),
		Timestamp: time.Now().Unix(),
		Source:    "database",
		Role:      p.role,
		Services:  cfg.Services,
		Plugins:   cfg.Plugins,
		Security:  cfg.Security,
		Server:    cfg.Server,
		Metadata:  make(map[string]interface{}),
	}, nil
}

func (p *DatabaseProvider) Watch(ctx context.Context, callback func(*ConfigSnapshot) error) error {
	return p.db.WatchConfig(ctx, func(data []byte) error {
		var cfg config.Config
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("failed to unmarshal config: %w", err)
		}

		snapshot := &ConfigSnapshot{
			Version:   "db-" + time.Now().Format("20060102150405"),
			Timestamp: time.Now().Unix(),
			Source:    "database",
			Role:      p.role,
			Services:  cfg.Services,
			Plugins:   cfg.Plugins,
			Security:  cfg.Security,
			Server:    cfg.Server,
			Metadata:  make(map[string]interface{}),
		}

		return callback(snapshot)
	})
}

func (p *DatabaseProvider) Close() error {
	return p.db.Close()
}
