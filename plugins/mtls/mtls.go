package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
)

// MTLSPlugin implements client certificate authentication
type MTLSPlugin struct {
	name          string
	priority      int
	timeout       time.Duration
	failure       engine.FailureMode
	enabled       bool
	caFile        string
	caPool        *x509.CertPool
	allowedCNs    []string
	allowedOUs    []string
	skipPaths     []string
	claimsContext string
	mu            sync.RWMutex
	logger        *logging.Logger
}

// ClientCertInfo contains information extracted from the client certificate
type ClientCertInfo struct {
	Subject   string   `json:"subject"`
	Issuer    string   `json:"issuer"`
	CN        string   `json:"cn"`
	OU        []string `json:"ou"`
	O         []string `json:"o"`
	Serial    string   `json:"serial"`
	NotBefore string   `json:"not_before"`
	NotAfter  string   `json:"not_after"`
}

// NewMTLSPlugin creates a new mTLS plugin
func NewMTLSPlugin(logger *logging.Logger) *MTLSPlugin {
	return &MTLSPlugin{
		name:          "mtls",
		priority:      95, // High priority security plugin
		timeout:       2 * time.Second,
		failure:       engine.FailureModeDeny,
		claimsContext: "mtls_claims",
		logger:        logger,
	}
}

func (p *MTLSPlugin) Name() string { return p.name }

func (p *MTLSPlugin) Phase() engine.Phase {
	return engine.PhaseAuth
}

func (p *MTLSPlugin) Priority() int {
	return p.priority
}

func (p *MTLSPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *MTLSPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *MTLSPlugin) Initialize(config map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	}

	if !p.enabled {
		return nil
	}

	if caFile, ok := config["ca_file"].(string); ok {
		p.caFile = caFile
	}

	if allowedCNs, ok := config["allowed_cns"].([]interface{}); ok {
		for _, cn := range allowedCNs {
			if cnStr, ok := cn.(string); ok {
				p.allowedCNs = append(p.allowedCNs, cnStr)
			}
		}
	}

	if allowedOUs, ok := config["allowed_ous"].([]interface{}); ok {
		for _, ou := range allowedOUs {
			if ouStr, ok := ou.(string); ok {
				p.allowedOUs = append(p.allowedOUs, ouStr)
			}
		}
	}

	if skipPaths, ok := config["skip_paths"].([]interface{}); ok {
		for _, path := range skipPaths {
			if pathStr, ok := path.(string); ok {
				p.skipPaths = append(p.skipPaths, pathStr)
			}
		}
	}

	if claimsContext, ok := config["claims_context"].(string); ok {
		p.claimsContext = claimsContext
	}

	// Load CA certificate pool
	if err := p.loadCACertPool(); err != nil {
		return fmt.Errorf("failed to load CA certificate pool: %w", err)
	}

	p.logger.Info("mTLS plugin initialized",
		logging.String("ca_file", p.caFile),
		logging.Int("allowed_cns", len(p.allowedCNs)),
	)
	return nil
}

func (p *MTLSPlugin) loadCACertPool() error {
	if p.caFile == "" {
		return fmt.Errorf("ca_file is required for mTLS plugin")
	}

	caCert, err := os.ReadFile(p.caFile)
	if err != nil {
		return fmt.Errorf("failed to read CA certificate: %w", err)
	}

	p.caPool = x509.NewCertPool()
	if !p.caPool.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("failed to append CA certificate to pool")
	}

	return nil
}

func (p *MTLSPlugin) Execute(ctx *engine.ExecutionContext) error {
	if !p.enabled {
		return nil
	}

	// Extract HTTP request
	reqVal, ok := ctx.GetState("http_request")
	if !ok {
		return fmt.Errorf("http request not found in context")
	}
	r := reqVal.(*http.Request)

	// Skip mTLS validation for certain paths
	if p.shouldSkipValidation(r.URL.Path) {
		return nil
	}

	// Validate client certificate
	certInfo, err := p.validateClientCert(r)
	if err != nil {
		p.logger.Warn("mTLS validation failed",
			logging.Error(err),
			logging.String("path", r.URL.Path),
			logging.String("request_id", ctx.RequestID),
		)
		return err
	}

	// Add certificate info to execution context state
	ctx.SetState(p.claimsContext, certInfo)

	return nil
}

func (p *MTLSPlugin) shouldSkipValidation(path string) bool {
	for _, skipPath := range p.skipPaths {
		if strings.HasPrefix(path, skipPath) {
			return true
		}
	}
	return false
}

func (p *MTLSPlugin) validateClientCert(r *http.Request) (*ClientCertInfo, error) {
	// Check if TLS connection exists
	if r.TLS == nil {
		return nil, fmt.Errorf("no TLS connection")
	}

	// Check if client certificate is present
	if len(r.TLS.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no client certificate provided")
	}

	clientCert := r.TLS.PeerCertificates[0]

	// Verify certificate against CA pool
	opts := x509.VerifyOptions{
		Roots:         p.caPool,
		CurrentTime:   time.Now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		Intermediates: x509.NewCertPool(),
	}

	// Add intermediate certificates to the pool
	for _, cert := range r.TLS.PeerCertificates[1:] {
		opts.Intermediates.AddCert(cert)
	}

	// Verify the certificate chain
	if _, err := clientCert.Verify(opts); err != nil {
		return nil, fmt.Errorf("certificate verification failed: %w", err)
	}

	// Check allowed Common Names
	if len(p.allowedCNs) > 0 {
		cnAllowed := false
		for _, allowedCN := range p.allowedCNs {
			if clientCert.Subject.CommonName == allowedCN {
				cnAllowed = true
				break
			}
		}
		if !cnAllowed {
			return nil, fmt.Errorf("common name not allowed: %s", clientCert.Subject.CommonName)
		}
	}

	// Check allowed Organizational Units
	if len(p.allowedOUs) > 0 {
		ouAllowed := false
		for _, allowedOU := range p.allowedOUs {
			for _, ou := range clientCert.Subject.OrganizationalUnit {
				if ou == allowedOU {
					ouAllowed = true
					break
				}
			}
			if ouAllowed {
				break
			}
		}
		if !ouAllowed {
			return nil, fmt.Errorf("organizational unit not allowed")
		}
	}

	// Create certificate info
	certInfo := &ClientCertInfo{
		Subject:   clientCert.Subject.String(),
		Issuer:    clientCert.Issuer.String(),
		CN:        clientCert.Subject.CommonName,
		OU:        clientCert.Subject.OrganizationalUnit,
		O:         clientCert.Subject.Organization,
		Serial:    clientCert.SerialNumber.String(),
		NotBefore: clientCert.NotBefore.Format("2006-01-02T15:04:05Z"),
		NotAfter:  clientCert.NotAfter.Format("2006-01-02T15:04:05Z"),
	}

	return certInfo, nil
}

// GetTLSConfig returns the TLS configuration for the server
func (p *MTLSPlugin) GetTLSConfig() *tls.Config {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if !p.enabled || p.caPool == nil {
		return nil
	}

	return &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  p.caPool,
	}
}
