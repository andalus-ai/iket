package runtime

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bhangun/iket/pkg/config"
	"github.com/bhangun/iket/pkg/core/compiler"
	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/core/provider"
	"github.com/bhangun/iket/pkg/logging"
	"github.com/bhangun/iket/pkg/metrics"
	"github.com/bhangun/iket/pkg/plugin"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// Gateway represents the refactored, phase-based API gateway
type Gateway struct {
	// Configuration management
	configManager *provider.ConfigManager
	compiler      *compiler.Compiler

	// Execution engine
	engine *engine.Engine

	// Current compiled configuration (atomic swap)
	current atomic.Value // *compiler.CompiledConfig

	// HTTP routing
	router *mux.Router
	server *http.Server

	// Dependencies
	logger   *logging.Logger
	metrics  *metrics.Collector
	registry *plugin.Registry

	// Lifecycle management
	version    string
	startTime  time.Time
	shutdown   chan struct{}
	shutdownMu sync.Mutex

	// Configuration versioning and rollback
	configHistory []*compiler.CompiledConfig
	historyMu     sync.RWMutex
	maxHistory    int
}

type contextKey string

const (
	requestIDKey contextKey = "request_id"
)

// Dependencies contains all the dependencies required to create a Gateway
type Dependencies struct {
	ConfigManager *provider.ConfigManager
	Logger        *logging.Logger
	Metrics       *metrics.Collector
	Registry      *plugin.Registry
}

// NewGateway creates a new phase-based gateway instance
func NewGateway(deps Dependencies, version string) (*Gateway, error) {
	if deps.ConfigManager == nil {
		return nil, fmt.Errorf("config manager is required")
	}

	if deps.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	if deps.Registry == nil {
		return nil, fmt.Errorf("plugin registry is required")
	}

	// Create compiler
	comp := compiler.NewCompiler(deps.Logger, deps.Registry)

	// Create execution engine (no registry - pure executor)
	eng := engine.NewEngine(deps.Logger, deps.Metrics)

	gateway := &Gateway{
		configManager: deps.ConfigManager,
		compiler:      comp,
		engine:        eng,
		router:        mux.NewRouter(),
		logger:        deps.Logger,
		metrics:       deps.Metrics,
		registry:      deps.Registry,
		version:       version,
		startTime:     time.Now(),
		shutdown:      make(chan struct{}),
		configHistory: make([]*compiler.CompiledConfig, 0),
		maxHistory:    10,
	}

	// Load initial configuration
	if err := gateway.loadInitialConfig(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to load initial config: %w", err)
	}

	// Setup HTTP router
	if err := gateway.setupRouter(); err != nil {
		return nil, fmt.Errorf("failed to setup router: %w", err)
	}

	// Register for config changes
	deps.ConfigManager.OnChange(gateway.handleConfigChange)

	gateway.logger.Info("Gateway initialized",
		logging.String("version", version),
	)

	return gateway, nil
}

// loadInitialConfig loads and compiles the initial configuration
func (g *Gateway) loadInitialConfig(ctx context.Context) error {
	// Load snapshot from config manager
	snapshot, err := g.configManager.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to load config snapshot: %w", err)
	}

	// Compile configuration
	compiled, err := g.compiler.Compile(snapshot)
	if err != nil {
		return fmt.Errorf("compilation failed: %w", err)
	}

	// Apply compiled configuration
	g.applyConfig(compiled)

	return nil
}

// handleConfigChange processes configuration changes at runtime
func (g *Gateway) handleConfigChange(snapshot *provider.ConfigSnapshot) error {
	g.logger.Info("Processing configuration change",
		logging.String("version", snapshot.Version),
	)

	// Compile new configuration
	compiled, err := g.compiler.Compile(snapshot)
	if err != nil {
		g.logger.Error("Failed to compile new configuration", err)
		return fmt.Errorf("compilation failed: %w", err)
	}

	// Get current config for diff
	current := g.getCurrentConfig()

	// Generate diff
	diff := g.compiler.Diff(current, compiled)

	if !diff.HasChanges() {
		g.logger.Info("No configuration changes detected")
		return nil
	}

	g.logger.Info("Configuration changes detected",
		logging.Int("added", len(diff.Added)),
		logging.Int("removed", len(diff.Removed)),
		logging.Int("modified", len(diff.Modified)),
	)

	// Apply new configuration atomically
	g.applyConfig(compiled)

	return nil
}

// applyConfig atomically swaps the current configuration
func (g *Gateway) applyConfig(compiled *compiler.CompiledConfig) {
	// Store in history
	g.historyMu.Lock()
	g.configHistory = append(g.configHistory, compiled)
	if len(g.configHistory) > g.maxHistory {
		g.configHistory = g.configHistory[1:]
	}
	g.historyMu.Unlock()

	// Atomic swap
	g.current.Store(compiled)

	g.logger.Info("Configuration applied",
		logging.String("version", compiled.Version),
		logging.Int("routes", len(compiled.Routes)),
	)
}

// GetConfig retrieves the current active configuration as a *config.Config
func (g *Gateway) GetConfig() *config.Config {
	snapshot := g.configManager.Current()
	if snapshot == nil {
		return nil
	}
	return &config.Config{
		Server:   snapshot.Server,
		Security: snapshot.Security,
		Services: snapshot.Services,
		Plugins:  snapshot.Plugins,
	}
}

// GetRouter returns the gateway's HTTP router
func (g *Gateway) GetRouter() *mux.Router {
	return g.router
}

// GetCompiledConfig retrieves the current active compiled configuration
func (g *Gateway) GetCompiledConfig() *compiler.CompiledConfig {
	return g.getCurrentConfig()
}

// UpdateConfig updates the gateway configuration
func (g *Gateway) UpdateConfig(cfg *config.Config) error {
	// Create snapshot from config
	snapshot := &provider.ConfigSnapshot{
		Version:   fmt.Sprintf("api-%d", time.Now().Unix()),
		Timestamp: time.Now().Unix(),
		Source:    "api",
		Role:      provider.RoleRuntime,
		Services:  cfg.Services,
		Plugins:   cfg.Plugins,
		Security:  cfg.Security,
		Server:    cfg.Server,
	}

	return g.handleConfigChange(snapshot)
}

// getCurrentConfig retrieves the current active configuration
func (g *Gateway) getCurrentConfig() *compiler.CompiledConfig {
	if cfg := g.current.Load(); cfg != nil {
		return cfg.(*compiler.CompiledConfig)
	}
	return nil
}

// setupRouter configures HTTP routes and middleware
func (g *Gateway) setupRouter() error {
	// Global middleware
	g.router.Use(g.requestIDMiddleware())
	g.router.Use(g.accessLogMiddleware())
	g.router.Use(g.metricsMiddleware())
	g.router.Use(g.recoveryMiddleware())

	// Health and admin endpoints
	g.router.HandleFunc("/health", g.healthHandler).Methods("GET")
	g.router.HandleFunc("/ready", g.readinessHandler).Methods("GET")
	g.router.HandleFunc("/metrics", g.metricsHandler).Methods("GET")

	// Main request handler (handles all configured routes)
	g.router.PathPrefix("/").HandlerFunc(g.handleRequest)

	return nil
}

// handleRequest is the main request handler using phase-based execution
func (g *Gateway) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Get current config
	cfg := g.getCurrentConfig()
	if cfg == nil {
		g.writeError(w, "Gateway not configured", http.StatusServiceUnavailable)
		return
	}

	// Find matching route
	route := g.matchRoute(cfg, r)
	if route == nil {
		g.writeError(w, "Route not found", http.StatusNotFound)
		return
	}

	// Check if route is enabled
	if !route.Enabled {
		g.writeError(w, "Route disabled", http.StatusServiceUnavailable)
		return
	}

	// Use HTTP middleware to handle the request
	middleware := NewHTTPMiddleware(g.engine, g.compiler, g.logger)
	if err := middleware.HandleRequest(w, r, route); err != nil {
		g.logger.Error("Request handling failed",
			err,
			logging.String("route", route.ID),
		)
	}
}

// matchRoute finds the best matching route for a request
func (g *Gateway) matchRoute(cfg *compiler.CompiledConfig, r *http.Request) *compiler.CompiledRoute {
	path := r.URL.Path
	method := r.Method

	// Simple matching - in production, use more sophisticated matching
	// (wildcard support, regex, etc.)
	for _, route := range cfg.Routes {
		// Check path
		if route.Path != path {
			// TODO: Add wildcard and regex matching
			continue
		}

		// Check method
		methodMatch := false
		for _, m := range route.Methods {
			if m == method {
				methodMatch = true
				break
			}
		}

		if methodMatch {
			return route
		}
	}

	return nil
}

// Middleware implementations

// requestIDMiddleware adds a unique request ID
func (g *Gateway) requestIDMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = uuid.New().String()
			}

			w.Header().Set("X-Request-ID", requestID)
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// accessLogMiddleware logs all requests
func (g *Gateway) accessLogMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := &responseWriter{ResponseWriter: w, statusCode: 200}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)
			requestID, _ := r.Context().Value(requestIDKey).(string)

			g.logger.Info("HTTP request",
				logging.String("request_id", requestID),
				logging.String("method", r.Method),
				logging.String("path", r.URL.Path),
				logging.String("client_ip", GetClientIP(r)),
				logging.Int("status", wrapped.statusCode),
				logging.Duration("duration", duration),
			)
		})
	}
}

// metricsMiddleware tracks request metrics
func (g *Gateway) metricsMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if g.metrics == nil {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			wrapped := &responseWriter{ResponseWriter: w, statusCode: 200}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start).Seconds()
			g.metrics.RecordRequest(r.Method, r.URL.Path, wrapped.statusCode, duration)
		})
	}
}

// recoveryMiddleware recovers from panics
func (g *Gateway) recoveryMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					requestID, _ := r.Context().Value(requestIDKey).(string)

					g.logger.Error("Request panic recovered",
						fmt.Errorf("%v", err),
						logging.String("request_id", requestID),
						logging.String("path", r.URL.Path),
					)

					g.writeError(w, "Internal server error", http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// Health and admin handlers

// healthHandler returns the gateway health status
func (g *Gateway) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"UP","version":"%s","uptime":"%s"}`,
		g.version,
		time.Since(g.startTime).String(),
	)
}

// readinessHandler checks if the gateway is ready to serve traffic
func (g *Gateway) readinessHandler(w http.ResponseWriter, r *http.Request) {
	cfg := g.getCurrentConfig()
	if cfg == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"ready":false,"reason":"no configuration loaded"}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"ready":true,"routes":%d}`, len(cfg.Routes))
}

// metricsHandler exposes Prometheus metrics
func (g *Gateway) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if g.metrics != nil {
		g.metrics.ServeHTTP(w, r)
	} else {
		w.WriteHeader(http.StatusNotImplemented)
	}
}

// ValidateConfig performs dry-run validation of a configuration snapshot
func (g *Gateway) ValidateConfig(snapshot *provider.ConfigSnapshot) (*compiler.DryRunResult, error) {
	return g.compiler.DryRun(snapshot)
}

// Rollback reverts to a previous configuration version
func (g *Gateway) Rollback(version string) error {
	g.historyMu.RLock()
	defer g.historyMu.RUnlock()

	for _, cfg := range g.configHistory {
		if cfg.Version == version {
			g.applyConfig(cfg)
			g.logger.Info("Configuration rolled back",
				logging.String("version", version),
			)
			return nil
		}
	}

	return fmt.Errorf("version not found in history: %s", version)
}

// GetConfigHistory returns the configuration version history
func (g *Gateway) GetConfigHistory() []string {
	g.historyMu.RLock()
	defer g.historyMu.RUnlock()

	versions := make([]string, len(g.configHistory))
	for i, cfg := range g.configHistory {
		versions[i] = cfg.Version
	}
	return versions
}

// Serve starts the HTTP server
func (g *Gateway) Serve(ctx context.Context) error {
	cfg := g.getCurrentConfig()
	if cfg == nil || cfg.Globals == nil {
		return fmt.Errorf("no configuration loaded")
	}

	port := cfg.Globals.Port
	if port == 0 {
		port = 8080
	}

	g.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: g.router,
	}

	g.logger.Info("Starting HTTP server",
		logging.Int("port", int(port)),
	)

	// Start config watching
	go g.configManager.Watch(ctx)

	// Start server
	errChan := make(chan error, 1)
	go func() {
		if err := g.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Wait for shutdown or error
	select {
	case err := <-errChan:
		return err
	case <-g.shutdown:
		return g.server.Shutdown(ctx)
	case <-ctx.Done():
		return g.server.Shutdown(context.Background())
	}
}

// Shutdown gracefully stops the gateway
func (g *Gateway) Shutdown() {
	g.shutdownMu.Lock()
	defer g.shutdownMu.Unlock()

	select {
	case <-g.shutdown:
		return
	default:
		close(g.shutdown)
	}

	g.logger.Info("Gateway shutting down")
	g.configManager.Close()
}

// Utility functions

// GetClientIP extracts client IP from request
func GetClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Use RemoteAddr as fallback
	return r.RemoteAddr
}

// writeError writes a JSON error response
func (g *Gateway) writeError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":"%s","code":%d}`, message, code)
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
