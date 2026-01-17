package websocket

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
	"github.com/gorilla/websocket"
)

// WebSocketPlugin implements WebSocket proxying
type WebSocketPlugin struct {
	name           string
	priority       int
	timeout        time.Duration
	failure        engine.FailureMode
	enabled        bool
	upstreamURL    string
	skipPaths      []string
	pingInterval   time.Duration
	pongWait       time.Duration
	writeWait      time.Duration
	maxMessageSize int64
	mu             sync.RWMutex
	logger         *logging.Logger
}

// NewWebSocketPlugin creates a new WebSocket plugin
func NewWebSocketPlugin(logger *logging.Logger) *WebSocketPlugin {
	return &WebSocketPlugin{
		name:           "websocket",
		priority:       50,
		timeout:        0, // No timeout for WS
		failure:        engine.FailureModeDeny,
		pingInterval:   30 * time.Second,
		pongWait:       60 * time.Second,
		writeWait:      10 * time.Second,
		maxMessageSize: 512,
		logger:         logger,
	}
}

func (p *WebSocketPlugin) Name() string { return p.name }

func (p *WebSocketPlugin) Phase() engine.Phase {
	return engine.PhaseTraffic
}

func (p *WebSocketPlugin) Priority() int {
	return p.priority
}

func (p *WebSocketPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *WebSocketPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *WebSocketPlugin) Initialize(config map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	}

	if !p.enabled {
		return nil
	}

	if upstreamURL, ok := config["upstream_url"].(string); ok {
		p.upstreamURL = upstreamURL
	}

	if skipPaths, ok := config["skip_paths"].([]interface{}); ok {
		for _, path := range skipPaths {
			if pathStr, ok := path.(string); ok {
				p.skipPaths = append(p.skipPaths, pathStr)
			}
		}
	}

	if pingInterval, ok := config["ping_interval"].(float64); ok {
		p.pingInterval = time.Duration(pingInterval) * time.Second
	}

	if p.upstreamURL == "" {
		return fmt.Errorf("upstream_url is required for WebSocket plugin")
	}

	p.logger.Info("WebSocket plugin initialized",
		logging.String("upstream_url", p.upstreamURL),
	)
	return nil
}

func (p *WebSocketPlugin) Execute(ctx *engine.ExecutionContext) error {
	if !p.enabled {
		return nil
	}

	// Extract HTTP request and response
	reqVal, ok := ctx.GetState("http_request")
	if !ok {
		return fmt.Errorf("http request not found in context")
	}
	r := reqVal.(*http.Request)

	respVal, ok := ctx.GetState("http_response")
	if !ok {
		return fmt.Errorf("http response not found in context")
	}
	w := respVal.(http.ResponseWriter)

	// Check if this is a WebSocket upgrade request
	if !p.isWebSocketRequest(r) {
		return nil
	}

	// Proxy WebSocket
	return p.handleWebSocket(w, r, ctx)
}

func (p *WebSocketPlugin) isWebSocketRequest(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket")
}

func (p *WebSocketPlugin) handleWebSocket(w http.ResponseWriter, r *http.Request, ctx *engine.ExecutionContext) error {
	// Create upstream URL
	upstreamURL, err := p.buildUpstreamURL(r)
	if err != nil {
		return fmt.Errorf("failed to build upstream URL: %w", err)
	}

	// Create WebSocket dialer
	dialer := websocket.Dialer{
		HandshakeTimeout: 45 * time.Second,
	}

	// Connect to upstream
	upstreamConn, _, err := dialer.Dial(upstreamURL, p.buildHeaders(r))
	if err != nil {
		return fmt.Errorf("failed to connect to upstream: %w", err)
	}
	defer upstreamConn.Close()

	// Upgrade client connection
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for now
		},
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return fmt.Errorf("failed to upgrade client connection: %w", err)
	}
	defer clientConn.Close()

	// Coordination channels
	errChan := make(chan error, 2)

	// Proxy loops
	go p.proxy(clientConn, upstreamConn, errChan)
	go p.proxy(upstreamConn, clientConn, errChan)

	// Set a flag that response is handled
	ctx.SetState("response_written", true)

	// Wait for error or close
	err = <-errChan
	if err != nil && !websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
		p.logger.Error("WebSocket proxy error", err)
	}

	return nil
}

func (p *WebSocketPlugin) proxy(dst, src *websocket.Conn, errChan chan error) {
	for {
		messageType, message, err := src.ReadMessage()
		if err != nil {
			errChan <- err
			return
		}
		if err := dst.WriteMessage(messageType, message); err != nil {
			errChan <- err
			return
		}
	}
}

func (p *WebSocketPlugin) buildUpstreamURL(r *http.Request) (string, error) {
	baseURL, err := url.Parse(p.upstreamURL)
	if err != nil {
		return "", err
	}
	baseURL.Path = r.URL.Path
	baseURL.RawQuery = r.URL.RawQuery
	return baseURL.String(), nil
}

func (p *WebSocketPlugin) buildHeaders(r *http.Request) http.Header {
	headers := make(http.Header)
	for key, values := range r.Header {
		if key == "Connection" || key == "Upgrade" || key == "Sec-WebSocket-Key" ||
			key == "Sec-WebSocket-Version" || key == "Sec-WebSocket-Protocol" {
			continue
		}
		headers[key] = values
	}
	headers.Set("Connection", "Upgrade")
	headers.Set("Upgrade", "websocket")
	return headers
}
