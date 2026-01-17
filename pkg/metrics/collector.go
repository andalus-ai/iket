package metrics

import (
	"fmt"
	"net/http"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Collector handles metrics collection for the gateway
type Collector struct {
	registry *prometheus.Registry

	// HTTP metrics
	httpRequestsTotal    *prometheus.CounterVec
	httpRequestDuration  *prometheus.HistogramVec
	httpRequestsInFlight *prometheus.GaugeVec

	// Engine metrics
	phaseDuration  *prometheus.HistogramVec
	pluginDuration *prometheus.HistogramVec

	// Gateway metrics
	activeConnections prometheus.Gauge
	configReloads     prometheus.Counter
}

// NewCollector creates a new metrics collector
func NewCollector() *Collector {
	registry := prometheus.NewRegistry()

	collector := &Collector{
		registry: registry,
		httpRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),
		httpRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gateway_http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		httpRequestsInFlight: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gateway_http_requests_in_flight",
				Help: "Current number of HTTP requests being processed",
			},
			[]string{"method", "path"},
		),
		activeConnections: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "gateway_active_connections",
				Help: "Current number of active connections",
			},
		),
		configReloads: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "gateway_config_reloads_total",
				Help: "Total number of configuration reloads",
			},
		),
		phaseDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gateway_phase_duration_seconds",
				Help:    "Execution duration of each phase in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"phase", "success"},
		),
		pluginDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gateway_plugin_duration_seconds",
				Help:    "Execution duration of each plugin in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"plugin", "phase", "success"},
		),
	}

	// Register metrics
	registry.MustRegister(
		collector.httpRequestsTotal,
		collector.httpRequestDuration,
		collector.httpRequestsInFlight,
		collector.activeConnections,
		collector.configReloads,
		collector.phaseDuration,
		collector.pluginDuration,
	)

	return collector
}

// ServeHTTP implements http.Handler for metrics endpoint
func (c *Collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
}

// RecordRequest records an HTTP request
func (c *Collector) RecordRequest(method, path string, status int, duration float64) {
	c.httpRequestsTotal.WithLabelValues(method, path, fmt.Sprintf("%d", status)).Inc()
	c.httpRequestDuration.WithLabelValues(method, path).Observe(duration)
}

// RecordPhaseExecution records engine phase metrics
func (c *Collector) RecordPhaseExecution(phase engine.Phase, duration time.Duration, success bool) {
	c.phaseDuration.WithLabelValues(string(phase), fmt.Sprintf("%t", success)).Observe(duration.Seconds())
}

// RecordPluginExecution records engine plugin metrics
func (c *Collector) RecordPluginExecution(plugin string, phase engine.Phase, duration time.Duration, success bool) {
	c.pluginDuration.WithLabelValues(plugin, string(phase), fmt.Sprintf("%t", success)).Observe(duration.Seconds())
}

// TrackRequestInFlight tracks a request in flight
func (c *Collector) TrackRequestInFlight(method, path string, inFlight bool) {
	if inFlight {
		c.httpRequestsInFlight.WithLabelValues(method, path).Inc()
	} else {
		c.httpRequestsInFlight.WithLabelValues(method, path).Dec()
	}
}

// SetActiveConnections sets the number of active connections
func (c *Collector) SetActiveConnections(count float64) {
	c.activeConnections.Set(count)
}

// IncrementConfigReloads increments the config reload counter
func (c *Collector) IncrementConfigReloads() {
	c.configReloads.Inc()
}
