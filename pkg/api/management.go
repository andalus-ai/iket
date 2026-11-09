package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/config"
	"github.com/bhangun/iket/pkg/logging"

	"github.com/bhangun/iket/pkg/core/runtime"
	"github.com/bhangun/iket/pkg/plugin"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// ManagementAPI provides REST endpoints for gateway management
type ManagementAPI struct {
	gateway  *runtime.Gateway
	logger   *logging.Logger
	registry *plugin.Registry
	security *config.SecurityConfig
	mu       sync.RWMutex

	// WebSocket upgrader
	upgrader websocket.Upgrader

	// Real-time update channels
	statusSubscribers  map[*websocket.Conn]bool
	metricsSubscribers map[*websocket.Conn]bool
	logsSubscribers    map[*websocket.Conn]bool
	subscriberMu       sync.RWMutex
}

// NewManagementAPI creates a new management API instance
func NewManagementAPI(gateway *runtime.Gateway, logger *logging.Logger, registry *plugin.Registry) *ManagementAPI {
	api := &ManagementAPI{
		gateway:  gateway,
		logger:   logger,
		registry: registry,
		upgrader: websocket.Upgrader{},
		statusSubscribers:  make(map[*websocket.Conn]bool),
		metricsSubscribers: make(map[*websocket.Conn]bool),
		logsSubscribers:    make(map[*websocket.Conn]bool),
	}

	// Start real-time update goroutines
	go api.broadcastStatusUpdates()
	go api.broadcastMetricsUpdates()

	return api
}

// RegisterRoutes registers all management API routes
func (api *ManagementAPI) RegisterRoutes(router *mux.Router) {
	// API v1 routes
	v1 := router.PathPrefix("/api/v1").Subrouter()

	// Management endpoints are privileged operator APIs. Require verified mTLS
	// and configured Basic Auth before any REST or WebSocket handler runs.
	v1.Use(api.managementSecurityMiddleware)

	// Gateway management
	v1.HandleFunc("/gateway/status", api.getGatewayStatus).Methods("GET")
	v1.HandleFunc("/gateway/config", api.getGatewayConfig).Methods("GET")
	v1.HandleFunc("/gateway/config", api.updateGatewayConfig).Methods("PUT")
	v1.HandleFunc("/gateway/reload", api.reloadGateway).Methods("POST")
	v1.HandleFunc("/gateway/metrics", api.getGatewayMetrics).Methods("GET")

	// Plugin management
	v1.HandleFunc("/plugins", api.listPlugins).Methods("GET")
	v1.HandleFunc("/plugins/{name}", api.getPluginDetails).Methods("GET")
	v1.HandleFunc("/plugins/{name}/config", api.updatePluginConfig).Methods("PUT")
	v1.HandleFunc("/plugins/{name}/enable", api.enablePlugin).Methods("POST")
	v1.HandleFunc("/plugins/{name}/disable", api.disablePlugin).Methods("POST")
	v1.HandleFunc("/plugins/{name}/health", api.getPluginHealth).Methods("GET")
	v1.HandleFunc("/plugins/{name}/status", api.getPluginStatus).Methods("GET")

	// Route management
	v1.HandleFunc("/routes", api.listRoutes).Methods("GET")
	v1.HandleFunc("/routes", api.createRoute).Methods("POST")
	v1.HandleFunc("/routes/{id}", api.getRouteDetails).Methods("GET")
	v1.HandleFunc("/routes/{id}", api.updateRoute).Methods("PUT")
	v1.HandleFunc("/routes/{id}", api.deleteRoute).Methods("DELETE")
	v1.HandleFunc("/routes/{id}/enable", api.enableRoute).Methods("POST")
	v1.HandleFunc("/routes/{id}/disable", api.disableRoute).Methods("POST")

	// Monitoring & logs
	v1.HandleFunc("/logs", api.getLogs).Methods("GET")
	v1.HandleFunc("/logs/stream", api.streamLogs).Methods("GET")
	v1.HandleFunc("/metrics/system", api.getSystemMetrics).Methods("GET")

	// WebSocket endpoints
	v1.HandleFunc("/ws/status", api.wsStatus).Methods("GET")
	v1.HandleFunc("/ws/metrics", api.wsMetrics).Methods("GET")
	v1.HandleFunc("/ws/logs", api.wsLogs).Methods("GET")

	// Certificate management
	v1.HandleFunc("/certificates", api.listCertificates).Methods("GET")
	v1.HandleFunc("/certificates", api.uploadCertificate).Methods("POST")
	v1.HandleFunc("/certificates/{id}", api.deleteCertificate).Methods("DELETE")

	// Backup & restore
	v1.HandleFunc("/backup", api.createBackup).Methods("POST")
	v1.HandleFunc("/backup", api.listBackups).Methods("GET")
	v1.HandleFunc("/backup/{id}/restore", api.restoreBackup).Methods("POST")

	// Service management
	v1.HandleFunc("/services", api.getServices).Methods("GET")
	v1.HandleFunc("/services", api.createService).Methods("POST")
	v1.HandleFunc("/services/{name}", api.updateService).Methods("PUT")
	v1.HandleFunc("/services/{name}", api.deleteService).Methods("DELETE")
}

func (api *ManagementAPI) getGatewayConfig(w http.ResponseWriter, r *http.Request) {
	api.mu.RLock()
	defer api.mu.RUnlock()

	cfg := api.gateway.GetConfig()
	if cfg == nil {
		api.writeError(w, "Configuration not available", http.StatusInternalServerError)
		return
	}

	// Redact sensitive information
	redactedConfig := *cfg
	if redactedConfig.Security.Jwt.Secret != "" {
		redactedConfig.Security.Jwt.Secret = "REDACTED"
	}
	redactedConfig.Security.BasicAuthUsers = nil

	api.writeJSON(w, redactedConfig)
}

func (api *ManagementAPI) updateGatewayConfig(w http.ResponseWriter, r *http.Request) {
	var newConfig config.Config
	if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
		api.writeError(w, "Invalid configuration format", http.StatusBadRequest)
		return
	}

	// Validate configuration
	if err := newConfig.Validate(); err != nil {
		api.writeError(w, "Invalid configuration", http.StatusBadRequest)
		return
	}

	// Update gateway configuration
	if err := api.gateway.UpdateConfig(&newConfig); err != nil {
		api.writeError(w, "Failed to update configuration", http.StatusInternalServerError)
		return
	}

	response := APIResponse{
		Success: true,
		Message: "Configuration updated successfully",
		Data: map[string]interface{}{
			"reload_required": true,
		},
	}
	api.writeJSON(w, response)
}

func (api *ManagementAPI) reloadGateway(w http.ResponseWriter, r *http.Request) {
	// This would trigger a configuration reload
	// For now, just return success

	response := APIResponse{
		Success: true,
		Message: "Configuration reloaded successfully",
		Data: map[string]interface{}{
			"timestamp": time.Now(),
		},
	}
	api.writeJSON(w, response)
}

// ListPlugins returns the list of registered plugin names.
func (m *ManagementAPI) ListPlugins() []string {
	return m.registry.List() // assuming Registry has a List() method
}

func (api *ManagementAPI) listPlugins(w http.ResponseWriter, r *http.Request) {
	plugins := api.registry.List()
	pluginInfos := make([]PluginInfo, 0, len(plugins))

	for _, name := range plugins {
		plugin, err := api.registry.Get(name)
		if err != nil {
			continue
		}

		info := PluginInfo{
			Name:    name,
			Type:    "unknown",
			Enabled: true,
			Status:  "healthy",
			Tags:    make(map[string]string),
		}

		// Get plugin type if available
		if typedPlugin, ok := plugin.(interface{ Type() string }); ok {
			info.Type = typedPlugin.Type()
		}

		// Get plugin tags if available
		if taggedPlugin, ok := plugin.(interface{ Tags() map[string]string }); ok {
			info.Tags = taggedPlugin.Tags()
		}

		// Check health if available
		if healthChecker, ok := plugin.(interface{ Health() error }); ok {
			if err := healthChecker.Health(); err != nil {
				info.Status = "unhealthy"
			}
		}

		pluginInfos = append(pluginInfos, info)
	}

	response := map[string]interface{}{
		"plugins": pluginInfos,
	}
	api.writeJSON(w, response)
}

func (api *ManagementAPI) getPluginDetails(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginName := vars["name"]

	plugin, err := api.registry.Get(pluginName)
	if err != nil {
		api.writeError(w, "Plugin not found", http.StatusNotFound)
		return
	}

	details := map[string]interface{}{
		"name":    pluginName,
		"type":    "unknown",
		"enabled": true,
		"status":  "healthy",
	}

	// Get plugin type
	if typedPlugin, ok := plugin.(interface{ Type() string }); ok {
		details["type"] = typedPlugin.Type()
	}

	// Get plugin tags
	if taggedPlugin, ok := plugin.(interface{ Tags() map[string]string }); ok {
		details["tags"] = taggedPlugin.Tags()
	}

	// Get health status
	if healthChecker, ok := plugin.(interface{ Health() error }); ok {
		if err := healthChecker.Health(); err != nil {
			details["status"] = "unhealthy"
			details["health"] = map[string]interface{}{
				"status":  "unhealthy",
				"message": err.Error(),
			}
		} else {
			details["health"] = map[string]interface{}{
				"status":  "healthy",
				"message": "Plugin is functioning normally",
			}
		}
	}

	// Get status
	if statusReporter, ok := plugin.(interface{ Status() string }); ok {
		details["status_message"] = statusReporter.Status()
	}

	api.writeJSON(w, details)
}

func (api *ManagementAPI) updatePluginConfig(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginName := vars["name"]

	var config map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		api.writeError(w, "Invalid configuration format", http.StatusBadRequest)
		return
	}

	plugin, err := api.registry.Get(pluginName)
	if err != nil {
		api.writeError(w, "Plugin not found", http.StatusNotFound)
		return
	}

	// Reload plugin with new configuration
	if err := plugin.Initialize(config); err != nil {
		api.writeError(w, "Failed to update plugin configuration", http.StatusInternalServerError)
		return
	}

	response := APIResponse{
		Success: true,
		Message: "Plugin configuration updated",
	}
	api.writeJSON(w, response)
}

func (api *ManagementAPI) enablePlugin(w http.ResponseWriter, r *http.Request) {
	_ = mux.Vars(r)["name"] // Extract but don't use for now

	// This would enable the plugin
	// For now, just return success

	response := APIResponse{
		Success: true,
		Message: "Plugin enabled successfully",
	}
	api.writeJSON(w, response)
}

func (api *ManagementAPI) disablePlugin(w http.ResponseWriter, r *http.Request) {
	_ = mux.Vars(r)["name"] // Extract but don't use for now

	// This would disable the plugin
	// For now, just return success

	response := APIResponse{
		Success: true,
		Message: "Plugin disabled successfully",
	}
	api.writeJSON(w, response)
}

func (api *ManagementAPI) getPluginHealth(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginName := vars["name"]

	plugin, err := api.registry.Get(pluginName)
	if err != nil {
		api.writeError(w, "Plugin not found", http.StatusNotFound)
		return
	}

	healthChecker, ok := plugin.(interface{ Health() error })
	if !ok {
		api.writeError(w, "Plugin does not support health checks", http.StatusNotImplemented)
		return
	}

	err = healthChecker.Health()
	health := map[string]interface{}{
		"status":     "healthy",
		"last_check": time.Now(),
		"message":    "Plugin is functioning normally",
	}

	if err != nil {
		health["status"] = "unhealthy"
		health["message"] = err.Error()
	}

	api.writeJSON(w, health)
}

func (api *ManagementAPI) getPluginStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pluginName := vars["name"]

	plugin, err := api.registry.Get(pluginName)
	if err != nil {
		api.writeError(w, "Plugin not found", http.StatusNotFound)
		return
	}

	statusReporter, ok := plugin.(interface{ Status() string })
	if !ok {
		api.writeError(w, "Plugin does not support status reporting", http.StatusNotImplemented)
		return
	}

	status := map[string]interface{}{
		"status":      statusReporter.Status(),
		"enabled":     true,
		"last_update": time.Now(),
	}

	api.writeJSON(w, status)
}

func (api *ManagementAPI) getLogs(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters (not used in placeholder implementation)
	_ = r.URL.Query().Get("limit")
	_ = r.URL.Query().Get("level")
	_ = r.URL.Query().Get("since")

	// This would fetch logs based on parameters
	// For now, return mock data

	logs := []map[string]interface{}{
		{
			"timestamp":  time.Now().Add(-5 * time.Minute),
			"level":      "info",
			"message":    "Request processed successfully",
			"route_id":   "route-1",
			"client_ip":  "192.168.1.100",
			"request_id": "req-12345",
		},
		{
			"timestamp":  time.Now().Add(-3 * time.Minute),
			"level":      "error",
			"message":    "Backend service unavailable",
			"route_id":   "route-1",
			"client_ip":  "192.168.1.101",
			"request_id": "req-12346",
		},
	}

	response := map[string]interface{}{
		"logs":     logs,
		"total":    len(logs),
		"has_more": false,
	}
	api.writeJSON(w, response)
}

func (api *ManagementAPI) streamLogs(w http.ResponseWriter, r *http.Request) {
	// Set headers for Server-Sent Events
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create a channel to signal client disconnect
	notify := w.(http.CloseNotifier).CloseNotify()

	// Send initial connection message
	fmt.Fprintf(w, "event: connected\ndata: {\"message\":\"Connected to log stream\"}\n\n")
	w.(http.Flusher).Flush()

	// Simulate log streaming
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-notify:
			return
		case <-ticker.C:
			logEntry := map[string]interface{}{
				"timestamp": time.Now(),
				"level":     "info",
				"message":   "Log entry from stream",
			}

			data, _ := json.Marshal(logEntry)
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
			w.(http.Flusher).Flush()
		}
	}
}

func (api *ManagementAPI) getSystemMetrics(w http.ResponseWriter, r *http.Request) {
	// This would get actual system metrics
	// For now, return mock data

	metrics := map[string]interface{}{
		"cpu": map[string]interface{}{
			"usage_percent": 25.5,
			"cores":         8,
		},
		"memory": map[string]interface{}{
			"total_mb":      16384,
			"used_mb":       8192,
			"usage_percent": 50.0,
		},
		"disk": map[string]interface{}{
			"total_gb":      500,
			"used_gb":       250,
			"usage_percent": 50.0,
		},
		"network": map[string]interface{}{
			"bytes_in":  1048576,
			"bytes_out": 2097152,
		},
	}

	api.writeJSON(w, metrics)
}

// WebSocket handlers

func (api *ManagementAPI) wsStatus(w http.ResponseWriter, r *http.Request) {
	conn, err := api.upgrader.Upgrade(w, r, nil)
	if err != nil {
		api.logger.Error("Failed to upgrade WebSocket connection", err)
		return
	}
	defer conn.Close()

	// Register subscriber
	api.subscriberMu.Lock()
	api.statusSubscribers[conn] = true
	api.subscriberMu.Unlock()

	// Remove subscriber when connection closes
	defer func() {
		api.subscriberMu.Lock()
		delete(api.statusSubscribers, conn)
		api.subscriberMu.Unlock()
	}()

	// Keep connection alive
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (api *ManagementAPI) wsMetrics(w http.ResponseWriter, r *http.Request) {
	conn, err := api.upgrader.Upgrade(w, r, nil)
	if err != nil {
		api.logger.Error("Failed to upgrade WebSocket connection", err)
		return
	}
	defer conn.Close()

	// Register subscriber
	api.subscriberMu.Lock()
	api.metricsSubscribers[conn] = true
	api.subscriberMu.Unlock()

	// Remove subscriber when connection closes
	defer func() {
		api.subscriberMu.Lock()
		delete(api.metricsSubscribers, conn)
		api.subscriberMu.Unlock()
	}()

	// Keep connection alive
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (api *ManagementAPI) wsLogs(w http.ResponseWriter, r *http.Request) {
	conn, err := api.upgrader.Upgrade(w, r, nil)
	if err != nil {
		api.logger.Error("Failed to upgrade WebSocket connection", err)
		return
	}
	defer conn.Close()

	// Register subscriber
	api.subscriberMu.Lock()
	api.logsSubscribers[conn] = true
	api.subscriberMu.Unlock()

	// Remove subscriber when connection closes
	defer func() {
		api.subscriberMu.Lock()
		delete(api.logsSubscribers, conn)
		api.subscriberMu.Unlock()
	}()

	// Keep connection alive
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// Real-time update broadcasters

func (api *ManagementAPI) broadcastStatusUpdates() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		status := map[string]interface{}{
			"type": "status_update",
			"data": map[string]interface{}{
				"status":             "running",
				"active_connections": 42,
				"total_requests":     15420,
			},
		}

		api.broadcastToSubscribers(api.statusSubscribers, status)
	}
}

func (api *ManagementAPI) broadcastMetricsUpdates() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		metrics := map[string]interface{}{
			"type": "metrics_update",
			"data": map[string]interface{}{
				"requests_per_minute": 120.0,
				"avg_response_time":   45.2,
				"error_rate":          0.26,
			},
		}

		api.broadcastToSubscribers(api.metricsSubscribers, metrics)
	}
}

func (api *ManagementAPI) broadcastToSubscribers(subscribers map[*websocket.Conn]bool, message interface{}) {
	data, err := json.Marshal(message)
	if err != nil {
		api.logger.Error("Failed to marshal message", err)
		return
	}

	api.subscriberMu.RLock()
	defer api.subscriberMu.RUnlock()

	for conn := range subscribers {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			api.logger.Error("Failed to send message to subscriber", err)
			// Remove failed connection
			delete(subscribers, conn)
			conn.Close()
		}
	}
}

// Certificate management (placeholder implementations)

func (api *ManagementAPI) listCertificates(w http.ResponseWriter, r *http.Request) {
	certificates := []map[string]interface{}{
		{
			"id":          "cert-1",
			"name":        "main-cert",
			"type":        "tls",
			"subject":     "CN=example.com",
			"issuer":      "CN=Let's Encrypt",
			"valid_from":  time.Now().AddDate(0, -1, 0),
			"valid_until": time.Now().AddDate(0, 2, 0),
			"status":      "valid",
		},
	}

	response := map[string]interface{}{
		"certificates": certificates,
	}
	api.writeJSON(w, response)
}

func (api *ManagementAPI) uploadCertificate(w http.ResponseWriter, r *http.Request) {
	// This would handle certificate upload
	// For now, return success

	response := APIResponse{
		Success: true,
		Message: "Certificate uploaded successfully",
		Data: map[string]interface{}{
			"certificate_id": "cert-2",
		},
	}
	api.writeJSON(w, response)
}

func (api *ManagementAPI) deleteCertificate(w http.ResponseWriter, r *http.Request) {
	_ = mux.Vars(r)["id"] // Extract but don't use for now

	// This would delete the certificate
	// For now, return success

	response := APIResponse{
		Success: true,
		Message: "Certificate deleted successfully",
	}
	api.writeJSON(w, response)
}

// Backup & restore (placeholder implementations)

// Helper methods

func (api *ManagementAPI) getServices(w http.ResponseWriter, r *http.Request) {
	cfg := api.gateway.GetConfig()
	if cfg == nil {
		api.writeError(w, "Configuration not available", http.StatusInternalServerError)
		return
	}

	// Redact sensitive info if needed (e.g., backend URLs, secrets)
	services := make([]map[string]interface{}, 0)
	for _, svcConfig := range cfg.Services {
		for _, svc := range svcConfig.Services {
			serviceInfo := map[string]interface{}{
				"name":        svc.Name,
				"description": svc.Description,
				"host":        svc.Host,
				"base_path":   svc.BasePath,
				"tags":        svc.Tags,
				"group":       svc.Group,
				"routes":      make([]map[string]interface{}, 0),
			}
			for _, route := range svc.Routes {
				routeInfo := map[string]interface{}{
					"path":        route.Path,
					"method":      route.Method,
					"name":        route.Name,
					"description": route.Description,
					"tags":        route.Tags,
					"group":       route.Group,
					"priority":    route.Priority,
					"enabled":     route.Enabled,
					// Do not include backend URLs or secrets
				}
				serviceInfo["routes"] = append(serviceInfo["routes"].([]map[string]interface{}), routeInfo)
			}
			services = append(services, serviceInfo)
		}
	}
	response := map[string]interface{}{
		"services": services,
	}
	api.writeJSON(w, response)
}

// POST /api/v1/services
func (api *ManagementAPI) createService(w http.ResponseWriter, r *http.Request) {
	cfg := api.gateway.GetConfig()
	if cfg == nil {
		api.writeError(w, "Configuration not available", http.StatusInternalServerError)
		return
	}
	var req struct {
		Services []config.Service `json:"services"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.writeError(w, "Invalid service definition", http.StatusBadRequest)
		return
	}
	if len(req.Services) == 0 {
		api.writeError(w, "No services provided", http.StatusBadRequest)
		return
	}
	if len(cfg.Services) == 0 {
		cfg.Services = []config.ServiceConfig{{Version: 1, Services: []config.Service{}}}
	}
	addedRoutes := []map[string]interface{}{}
	skippedRoutes := []map[string]interface{}{}
	for _, newSvc := range req.Services {
		found := false
		for i, svc := range cfg.Services[0].Services {
			if svc.Host == newSvc.Host { // merge by host
				found = true
				existingRoutes := svc.Routes
				for _, newRoute := range newSvc.Routes {
					dup := false
					for _, existRoute := range existingRoutes {
						if existRoute.Path == newRoute.Path && existRoute.Method == newRoute.Method {
							dup = true
							break
						}
					}
					if dup {
						skippedRoutes = append(skippedRoutes, map[string]interface{}{"path": newRoute.Path, "method": newRoute.Method, "reason": "duplicate"})
						continue
					}
					cfg.Services[0].Services[i].Routes = append(cfg.Services[0].Services[i].Routes, newRoute)
					addedRoutes = append(addedRoutes, map[string]interface{}{"path": newRoute.Path, "method": newRoute.Method})
				}
				break
			}
		}
		if !found {
			cfg.Services[0].Services = append(cfg.Services[0].Services, newSvc)
			for _, newRoute := range newSvc.Routes {
				addedRoutes = append(addedRoutes, map[string]interface{}{"path": newRoute.Path, "method": newRoute.Method})
			}
		}
	}
	api.gateway.UpdateConfig(cfg)
	msg := fmt.Sprintf("%d route(s) added, %d skipped due to duplication", len(addedRoutes), len(skippedRoutes))
	api.writeJSON(w, map[string]interface{}{
		"success":        true,
		"added_routes":   addedRoutes,
		"skipped_routes": skippedRoutes,
		"message":        msg,
	})
}

// PUT /api/v1/services/{name}
func (api *ManagementAPI) updateService(w http.ResponseWriter, r *http.Request) {
	cfg := api.gateway.GetConfig()
	if cfg == nil {
		api.writeError(w, "Configuration not available", http.StatusInternalServerError)
		return
	}
	name := mux.Vars(r)["name"]
	var update config.Service
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		api.writeError(w, "Invalid service definition", http.StatusBadRequest)
		return
	}
	updated := false
	addedRoutes := []map[string]interface{}{}
	updatedRoutes := []map[string]interface{}{}
	for i, svcConfig := range cfg.Services {
		for j, svc := range svcConfig.Services {
			if svc.Name == name {
				existingRoutes := svc.Routes
				for _, newRoute := range update.Routes {
					found := false
					for k, existRoute := range existingRoutes {
						if existRoute.Path == newRoute.Path && existRoute.Method == newRoute.Method {
							cfg.Services[i].Services[j].Routes[k] = newRoute
							updatedRoutes = append(updatedRoutes, map[string]interface{}{"path": newRoute.Path, "method": newRoute.Method})
							found = true
							break
						}
					}
					if !found {
						cfg.Services[i].Services[j].Routes = append(cfg.Services[i].Services[j].Routes, newRoute)
						addedRoutes = append(addedRoutes, map[string]interface{}{"path": newRoute.Path, "method": newRoute.Method})
					}
				}
				// Optionally update other service fields
				cfg.Services[i].Services[j].Description = update.Description
				cfg.Services[i].Services[j].Host = update.Host
				cfg.Services[i].Services[j].BasePath = update.BasePath
				cfg.Services[i].Services[j].Tags = update.Tags
				cfg.Services[i].Services[j].Group = update.Group
				updated = true
				break
			}
		}
	}
	if !updated {
		api.writeError(w, "Service not found", http.StatusNotFound)
		return
	}
	api.gateway.UpdateConfig(cfg)
	msg := fmt.Sprintf("%d route(s) updated, %d added", len(updatedRoutes), len(addedRoutes))
	api.writeJSON(w, map[string]interface{}{
		"success":        true,
		"updated_routes": updatedRoutes,
		"added_routes":   addedRoutes,
		"message":        msg,
	})
}

// DELETE /api/v1/services/{name}
func (api *ManagementAPI) deleteService(w http.ResponseWriter, r *http.Request) {
	cfg := api.gateway.GetConfig()
	if cfg == nil {
		api.writeError(w, "Configuration not available", http.StatusInternalServerError)
		return
	}
	name := mux.Vars(r)["name"]
	deleted := false
	for i, svcConfig := range cfg.Services {
		for j, svc := range svcConfig.Services {
			if svc.Name == name {
				cfg.Services[i].Services = append(cfg.Services[i].Services[:j], cfg.Services[i].Services[j+1:]...)
				deleted = true
				break
			}
		}
	}
	if !deleted {
		api.writeError(w, "Service not found", http.StatusNotFound)
		return
	}
	api.gateway.UpdateConfig(cfg)
	api.writeJSON(w, APIResponse{Success: true, Message: "Service deleted successfully"})
}
