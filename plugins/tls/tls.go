package tls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
)

// TLSPlugin implements server-side TLS configuration
type TLSPlugin struct {
	name         string
	priority     int
	timeout      time.Duration
	failure      engine.FailureMode
	enabled      bool
	certFile     string
	keyFile      string
	caFile       string
	clientAuth   bool
	minVersion   uint16
	maxVersion   uint16
	cipherSuites []uint16
	config       *tls.Config
	mu           sync.RWMutex
	logger       *logging.Logger
}

// NewTLSPlugin creates a new TLS plugin
func NewTLSPlugin(logger *logging.Logger) *TLSPlugin {
	return &TLSPlugin{
		name:       "tls",
		priority:   100, // Highest priority
		timeout:    1 * time.Second,
		failure:    engine.FailureModeDeny,
		minVersion: tls.VersionTLS12,
		maxVersion: tls.VersionTLS13,
		logger:     logger,
	}
}

func (p *TLSPlugin) Name() string { return p.name }

func (p *TLSPlugin) Phase() engine.Phase {
	return engine.PhaseTraffic
}

func (p *TLSPlugin) Priority() int {
	return p.priority
}

func (p *TLSPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *TLSPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *TLSPlugin) Initialize(config map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	}

	if !p.enabled {
		return nil
	}

	if certFile, ok := config["cert_file"].(string); ok {
		p.certFile = certFile
	}

	if keyFile, ok := config["key_file"].(string); ok {
		p.keyFile = keyFile
	}

	if caFile, ok := config["ca_file"].(string); ok {
		p.caFile = caFile
	}

	if clientAuth, ok := config["client_auth"].(bool); ok {
		p.clientAuth = clientAuth
	}

	if p.certFile == "" || p.keyFile == "" {
		return fmt.Errorf("cert_file and key_file are required for TLS")
	}

	if err := p.buildTLSConfig(); err != nil {
		return fmt.Errorf("failed to build TLS config: %w", err)
	}

	p.logger.Info("TLS plugin initialized",
		logging.String("cert_file", p.certFile),
		logging.Bool("client_auth", p.clientAuth),
	)
	return nil
}

func (p *TLSPlugin) buildTLSConfig() error {
	cert, err := tls.LoadX509KeyPair(p.certFile, p.keyFile)
	if err != nil {
		return fmt.Errorf("failed to load certificate: %w", err)
	}

	var caCertPool *x509.CertPool
	if p.caFile != "" {
		caCert, err := os.ReadFile(p.caFile)
		if err != nil {
			return fmt.Errorf("failed to read CA certificate: %w", err)
		}

		caCertPool = x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return fmt.Errorf("failed to append CA certificate")
		}
	}

	p.config = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   p.minVersion,
		MaxVersion:   p.maxVersion,
	}

	if p.clientAuth {
		p.config.ClientAuth = tls.RequireAndVerifyClientCert
		if caCertPool != nil {
			p.config.ClientCAs = caCertPool
		}
	}

	return nil
}

func (p *TLSPlugin) Execute(ctx *engine.ExecutionContext) error {
	return nil
}

func (p *TLSPlugin) GetTLSConfig() *tls.Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}
