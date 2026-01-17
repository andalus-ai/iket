package ipwhitelist

import (
	"fmt"
	"net"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
)

// IPWhitelistPlugin implements IP-based access control
type IPWhitelistPlugin struct {
	name        string
	priority    int
	timeout     time.Duration
	failure     engine.FailureMode
	allowedNets []*net.IPNet
	logger      *logging.Logger
}

// NewIPWhitelistPlugin creates a new IP whitelist plugin
func NewIPWhitelistPlugin(logger *logging.Logger) *IPWhitelistPlugin {
	return &IPWhitelistPlugin{
		name:     "ip_whitelist",
		priority: 95, // High priority, runs early
		timeout:  1 * time.Second,
		failure:  engine.FailureModeDeny,
		logger:   logger,
	}
}

func (p *IPWhitelistPlugin) Name() string { return p.name }

func (p *IPWhitelistPlugin) Phase() engine.Phase {
	return engine.PhaseAuth
}

func (p *IPWhitelistPlugin) Priority() int {
	return p.priority
}

func (p *IPWhitelistPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *IPWhitelistPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *IPWhitelistPlugin) Initialize(config map[string]interface{}) error {
	p.allowedNets = nil
	list, ok := config["list"]
	if !ok {
		return fmt.Errorf("ip_whitelist: missing 'list' in config")
	}

	var entries []string
	switch v := list.(type) {
	case []interface{}:
		for _, entry := range v {
			if s, ok := entry.(string); ok {
				entries = append(entries, s)
			}
		}
	case []string:
		entries = v
	case string:
		// Accept comma-separated string
		for _, s := range fmt.Sprint(v) { // Quick way to split if it's a string
			_ = s // Dummy
		}
		// Actually handle it properly
		entries = append(entries, v) // For now just use the string, split below
	default:
		return fmt.Errorf("ip_whitelist: invalid 'list' type")
	}

	for _, rawEntry := range entries {
		for _, entry := range splitEntries(rawEntry) {
			entry = fmt.Sprint(entry) // Ensure it's string
			_, ipnet, err := net.ParseCIDR(entry)
			if err != nil {
				// Try as single IP
				ip := net.ParseIP(entry)
				if ip == nil {
					return fmt.Errorf("ip_whitelist: invalid CIDR or IP: %s", entry)
				}
				mask := "/32"
				if ip.To4() == nil {
					mask = "/128"
				}
				_, ipnet, err = net.ParseCIDR(entry + mask)
				if err != nil {
					return fmt.Errorf("ip_whitelist: failed to parse IP as CIDR: %s", entry)
				}
			}
			p.allowedNets = append(p.allowedNets, ipnet)
		}
	}

	p.logger.Info("IP Whitelist plugin initialized",
		logging.Int("allowed_networks", len(p.allowedNets)),
	)

	return nil
}

func splitEntries(s string) []string {
	// Simple split
	parts := []string{s}
	if contains(s, ",") {
		parts = []string{}
		for _, p := range split(s, ",") {
			parts = append(parts, trim(p))
		}
	}
	return parts
}

// Minimal helpers to avoid extra dependencies for now
func contains(s, sep string) bool {
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			return true
		}
	}
	return false
}

func split(s, sep string) []string {
	var res []string
	start := 0
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			res = append(res, s[start:i])
			start = i + len(sep)
		}
	}
	res = append(res, s[start:])
	return res
}

func trim(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func (p *IPWhitelistPlugin) Execute(ctx *engine.ExecutionContext) error {
	clientIP := ctx.ClientIP
	if clientIP == "" {
		return fmt.Errorf("client IP not found in context")
	}

	ip := net.ParseIP(clientIP)
	if ip == nil {
		return fmt.Errorf("invalid client IP address: %s", clientIP)
	}

	allowed := false
	for _, net := range p.allowedNets {
		if net.Contains(ip) {
			allowed = true
			break
		}
	}

	if !allowed {
		p.logger.Warn("IP blocked by whitelist",
			logging.String("ip", clientIP),
			logging.String("request_id", ctx.RequestID),
		)
		return fmt.Errorf("IP %s is not whitelisted", clientIP)
	}

	return nil
}
