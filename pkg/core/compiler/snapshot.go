package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Snapshot represents an immutable configuration snapshot
type Snapshot struct {
	// Identity
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	Hash      string    `json:"hash"`
	Timestamp time.Time `json:"timestamp"`

	// Source metadata
	Source     string   `json:"source"`
	SourceType string   `json:"source_type"` // file, database, api
	MergedFrom []string `json:"merged_from,omitempty"`

	// Configuration data
	Services []ServiceConfig         `json:"services"`
	Plugins  map[string]PluginConfig `json:"plugins"`
	Security SecurityConfig          `json:"security"`
	Server   ServerConfig            `json:"server"`

	// Metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Labels   map[string]string      `json:"labels,omitempty"`

	// Lifecycle
	CreatedBy  string     `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ValidFrom  *time.Time `json:"valid_from,omitempty"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
}

// ServiceConfig represents a backend service
type ServiceConfig struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Host        string                 `json:"host"`
	BasePath    string                 `json:"base_path,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Group       string                 `json:"group,omitempty"`
	Routes      []RouteConfig          `json:"routes"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// RouteConfig represents a route configuration
type RouteConfig struct {
	Path        string   `json:"path"`
	Methods     []string `json:"methods"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Enabled     bool     `json:"enabled"`
	Priority    int      `json:"priority,omitempty"`

	// Plugin configuration
	Plugins []string `json:"plugins,omitempty"`

	// Route-specific settings
	Timeout   *Duration        `json:"timeout,omitempty"`
	RateLimit *RateLimitConfig `json:"rate_limit,omitempty"`

	// Metadata
	Tags     []string               `json:"tags,omitempty"`
	Group    string                 `json:"group,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// PluginConfig represents plugin configuration
type PluginConfig struct {
	Enabled  bool                   `json:"enabled"`
	Priority int                    `json:"priority,omitempty"`
	Config   map[string]interface{} `json:"config"`
}

// SecurityConfig represents security settings
type SecurityConfig struct {
	TLS         TLSConfig  `json:"tls,omitempty"`
	CORS        CORSConfig `json:"cors,omitempty"`
	IPWhitelist []string   `json:"ip_whitelist,omitempty"`
	IPBlacklist []string   `json:"ip_blacklist,omitempty"`
}

// TLSConfig represents TLS settings
type TLSConfig struct {
	Enabled    bool     `json:"enabled"`
	CertFile   string   `json:"cert_file,omitempty"`
	KeyFile    string   `json:"key_file,omitempty"`
	CAFile     string   `json:"ca_file,omitempty"`
	MinVersion string   `json:"min_version,omitempty"`
	Ciphers    []string `json:"ciphers,omitempty"`
	ClientAuth bool     `json:"client_auth,omitempty"`
}

// CORSConfig represents CORS settings
type CORSConfig struct {
	Enabled          bool     `json:"enabled"`
	AllowedOrigins   []string `json:"allowed_origins,omitempty"`
	AllowedMethods   []string `json:"allowed_methods,omitempty"`
	AllowedHeaders   []string `json:"allowed_headers,omitempty"`
	ExposeHeaders    []string `json:"expose_headers,omitempty"`
	MaxAge           int      `json:"max_age,omitempty"`
	AllowCredentials bool     `json:"allow_credentials,omitempty"`
}

// ServerConfig represents server settings
type ServerConfig struct {
	Port         int      `json:"port"`
	Host         string   `json:"host,omitempty"`
	ReadTimeout  Duration `json:"read_timeout,omitempty"`
	WriteTimeout Duration `json:"write_timeout,omitempty"`
	IdleTimeout  Duration `json:"idle_timeout,omitempty"`
}

// RateLimitConfig represents rate limiting settings
type RateLimitConfig struct {
	RequestsPerSecond float64 `json:"requests_per_second"`
	Burst             int     `json:"burst"`
}

// Duration is a wrapper for time.Duration that supports JSON
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}

	switch value := v.(type) {
	case float64:
		*d = Duration(time.Duration(value))
		return nil
	case string:
		tmp, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*d = Duration(tmp)
		return nil
	default:
		return fmt.Errorf("invalid duration")
	}
}

// NewSnapshot creates a new configuration snapshot
func NewSnapshot(source string, sourceType string) *Snapshot {
	now := time.Now()
	return &Snapshot{
		ID:         generateSnapshotID(),
		Version:    fmt.Sprintf("v%d", now.Unix()),
		Timestamp:  now,
		Source:     source,
		SourceType: sourceType,
		CreatedAt:  now,
		Services:   make([]ServiceConfig, 0),
		Plugins:    make(map[string]PluginConfig),
		Metadata:   make(map[string]interface{}),
		Labels:     make(map[string]string),
	}
}

// ComputeHash computes the SHA256 hash of the snapshot
func (s *Snapshot) ComputeHash() error {
	// Create a copy without hash and timestamp for stable hashing
	hashable := struct {
		Services []ServiceConfig         `json:"services"`
		Plugins  map[string]PluginConfig `json:"plugins"`
		Security SecurityConfig          `json:"security"`
		Server   ServerConfig            `json:"server"`
	}{
		Services: s.Services,
		Plugins:  s.Plugins,
		Security: s.Security,
		Server:   s.Server,
	}

	data, err := json.Marshal(hashable)
	if err != nil {
		return fmt.Errorf("failed to marshal snapshot: %w", err)
	}

	hash := sha256.Sum256(data)
	s.Hash = hex.EncodeToString(hash[:])

	return nil
}

// Validate performs basic validation on the snapshot
func (s *Snapshot) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("snapshot ID is required")
	}

	if s.Version == "" {
		return fmt.Errorf("snapshot version is required")
	}

	if len(s.Services) == 0 {
		return fmt.Errorf("at least one service is required")
	}

	// Validate each service
	for i, service := range s.Services {
		if err := s.validateService(&service); err != nil {
			return fmt.Errorf("service %d (%s): %w", i, service.Name, err)
		}
	}

	return nil
}

func (s *Snapshot) validateService(service *ServiceConfig) error {
	if service.Name == "" {
		return fmt.Errorf("service name is required")
	}

	if service.Host == "" {
		return fmt.Errorf("service host is required")
	}

	if len(service.Routes) == 0 {
		return fmt.Errorf("at least one route is required")
	}

	// Validate routes
	for i, route := range service.Routes {
		if err := s.validateRoute(&route); err != nil {
			return fmt.Errorf("route %d (%s): %w", i, route.Path, err)
		}
	}

	return nil
}

func (s *Snapshot) validateRoute(route *RouteConfig) error {
	if route.Path == "" {
		return fmt.Errorf("route path is required")
	}

	if len(route.Methods) == 0 {
		return fmt.Errorf("at least one HTTP method is required")
	}

	// Validate methods
	validMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "DELETE": true,
		"PATCH": true, "HEAD": true, "OPTIONS": true,
	}

	for _, method := range route.Methods {
		if !validMethods[method] {
			return fmt.Errorf("invalid HTTP method: %s", method)
		}
	}

	return nil
}

// Clone creates a deep copy of the snapshot
func (s *Snapshot) Clone() (*Snapshot, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal snapshot: %w", err)
	}

	var clone Snapshot
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil, fmt.Errorf("failed to unmarshal snapshot: %w", err)
	}

	return &clone, nil
}

// Diff compares two snapshots and returns differences
func (s *Snapshot) Diff(other *Snapshot) *SnapshotDiff {
	diff := &SnapshotDiff{
		From:            s.Version,
		To:              other.Version,
		ServicesAdded:   make([]string, 0),
		ServicesRemoved: make([]string, 0),
		ServicesChanged: make([]string, 0),
		RoutesAdded:     make([]string, 0),
		RoutesRemoved:   make([]string, 0),
		RoutesChanged:   make([]string, 0),
	}

	// Build service maps
	oldServices := make(map[string]*ServiceConfig)
	newServices := make(map[string]*ServiceConfig)

	for i := range s.Services {
		oldServices[s.Services[i].Name] = &s.Services[i]
	}

	for i := range other.Services {
		newServices[other.Services[i].Name] = &other.Services[i]
	}

	// Find added and changed services
	for name, newSvc := range newServices {
		if oldSvc, exists := oldServices[name]; exists {
			// Check if changed
			if !servicesEqual(oldSvc, newSvc) {
				diff.ServicesChanged = append(diff.ServicesChanged, name)
			}
		} else {
			diff.ServicesAdded = append(diff.ServicesAdded, name)
		}
	}

	// Find removed services
	for name := range oldServices {
		if _, exists := newServices[name]; !exists {
			diff.ServicesRemoved = append(diff.ServicesRemoved, name)
		}
	}

	diff.HasChanges = len(diff.ServicesAdded) > 0 ||
		len(diff.ServicesRemoved) > 0 ||
		len(diff.ServicesChanged) > 0

	return diff
}

// SnapshotDiff represents differences between snapshots
type SnapshotDiff struct {
	From            string   `json:"from"`
	To              string   `json:"to"`
	HasChanges      bool     `json:"has_changes"`
	ServicesAdded   []string `json:"services_added"`
	ServicesRemoved []string `json:"services_removed"`
	ServicesChanged []string `json:"services_changed"`
	RoutesAdded     []string `json:"routes_added"`
	RoutesRemoved   []string `json:"routes_removed"`
	RoutesChanged   []string `json:"routes_changed"`
}

// Helper functions

func generateSnapshotID() string {
	now := time.Now()
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d", now.UnixNano())))
	return hex.EncodeToString(hash[:])[:16]
}

func servicesEqual(a, b *ServiceConfig) bool {
	// Simple comparison - in production, do deep comparison
	return a.Host == b.Host && len(a.Routes) == len(b.Routes)
}

// Merge combines multiple snapshots with priority
func MergeSnapshots(snapshots []*Snapshot, strategy MergeStrategy) (*Snapshot, error) {
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("no snapshots to merge")
	}

	if len(snapshots) == 1 {
		return snapshots[0], nil
	}

	return strategy.Merge(snapshots)
}

// MergeStrategy defines how to merge snapshots
type MergeStrategy interface {
	Merge(snapshots []*Snapshot) (*Snapshot, error)
}

// OverlayMergeStrategy implements overlay merge (last wins)
type OverlayMergeStrategy struct{}

func (s *OverlayMergeStrategy) Merge(snapshots []*Snapshot) (*Snapshot, error) {
	base := snapshots[0]
	result, err := base.Clone()
	if err != nil {
		return nil, err
	}

	// Track merged sources
	result.MergedFrom = make([]string, len(snapshots))
	for i, snap := range snapshots {
		result.MergedFrom[i] = snap.Source
	}

	// Overlay subsequent snapshots
	for _, snap := range snapshots[1:] {
		// Services: append/update
		for _, newSvc := range snap.Services {
			found := false
			for i, existingSvc := range result.Services {
				if existingSvc.Name == newSvc.Name {
					result.Services[i] = newSvc
					found = true
					break
				}
			}
			if !found {
				result.Services = append(result.Services, newSvc)
			}
		}

		// Plugins: merge
		for name, pluginCfg := range snap.Plugins {
			result.Plugins[name] = pluginCfg
		}

		// Server: override
		result.Server = snap.Server

		// Security: override
		result.Security = snap.Security
	}

	// Update metadata
	result.ID = generateSnapshotID()
	result.Version = fmt.Sprintf("merged-%d", time.Now().Unix())
	result.Timestamp = time.Now()
	result.Source = "merged"
	result.SourceType = "merge"

	// Recompute hash
	if err := result.ComputeHash(); err != nil {
		return nil, err
	}

	return result, nil
}
