package metrics

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PrometheusPlugin implements metrics collection using Prometheus
type PrometheusPlugin struct {
	name            string
	priority        int
	timeout         time.Duration
	failure         engine.FailureMode
	metricsPath     string
	metricsPort     int
	server          *http.Server
	requestCounter  *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	responseSize    *prometheus.SummaryVec
	logger          *logging.Logger
}

// NewPrometheusPlugin creates a new Prometheus metrics plugin
func NewPrometheusPlugin(logger *logging.Logger) *PrometheusPlugin {
	return &PrometheusPlugin{
		name:     "prometheus",
		priority: 0, // Last priority in Log phase
		timeout:  1 * time.Second,
		failure:  engine.FailureModeFailOpen,
		logger:   logger,
	}
}

func (p *PrometheusPlugin) Name() string { return p.name }

func (p *PrometheusPlugin) Phase() engine.Phase {
	return engine.PhaseLog
}

func (p *PrometheusPlugin) Priority() int {
	return p.priority
}

func (p *PrometheusPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *PrometheusPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *PrometheusPlugin) Initialize(config map[string]interface{}) error {
	// Set default values
	p.metricsPath = "/metrics"
	p.metricsPort = 9090

	// Override with provided configuration
	if path, ok := config["metrics_path"].(string); ok {
		p.metricsPath = path
	}

	if port, ok := config["metrics_port"].(float64); ok {
		p.metricsPort = int(port)
	}

	// Initialize metrics (ensure they are only registered once if Initialize is called multiple times)
	// For simplicity, we assume one instance for now or use a global registry
	p.requestCounter = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	p.requestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	p.responseSize = promauto.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "gateway_http_response_size_bytes",
			Help:       "HTTP response size in bytes",
			Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		},
		[]string{"method", "path"},
	)

	// Start metrics server
	go p.startMetricsServer()

	p.logger.Info("Prometheus metrics plugin initialized",
		logging.Int("port", p.metricsPort),
		logging.String("path", p.metricsPath),
	)
	return nil
}

func (p *PrometheusPlugin) startMetricsServer() {
	mux := http.NewServeMux()
	mux.Handle(p.metricsPath, promhttp.Handler())

	p.server = &http.Server{
		Addr:    ":" + strconv.Itoa(p.metricsPort),
		Handler: mux,
	}

	if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		p.logger.Error("Failed to start metrics server", err)
	}
}

func (p *PrometheusPlugin) Execute(ctx *engine.ExecutionContext) error {
	// Metrics are usually recorded after the request is finished.
	// We can use context states to gather information.

	method, _ := ctx.GetState("request_method")
	path, _ := ctx.GetState("request_path")

	// Try to get status code from upstream_status or similar
	status := "200"
	if s, ok := ctx.GetState("upstream_status"); ok {
		status = strconv.Itoa(s.(int))
	} else if ctx.LastError != nil {
		status = "500"
	}

	p.requestCounter.WithLabelValues(fmt.Sprint(method), fmt.Sprint(path), status).Inc()
	p.requestDuration.WithLabelValues(fmt.Sprint(method), fmt.Sprint(path)).Observe(ctx.Duration().Seconds())

	// Response size handling would require more integration, skipping for now or use placeholder
	p.responseSize.WithLabelValues(fmt.Sprint(method), fmt.Sprint(path)).Observe(0)

	return nil
}

func (p *PrometheusPlugin) Close() error {
	if p.server != nil {
		return p.server.Close()
	}
	return nil
}
