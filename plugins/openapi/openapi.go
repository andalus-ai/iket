package openapi

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
	"gopkg.in/yaml.v3"
)

//go:embed swaggerui/*
var swaggerUIFS embed.FS

// OpenAPIPlugin implements OpenAPI documentation and Swagger UI
type OpenAPIPlugin struct {
	name      string
	priority  int
	timeout   time.Duration
	failure   engine.FailureMode
	specPath  string
	specData  []byte
	enabled   bool
	servePath string
	format    string // "yaml" or "json"
	swaggerUI bool
	logger    *logging.Logger
}

// NewOpenAPIPlugin creates a new OpenAPI Documentation plugin
func NewOpenAPIPlugin(logger *logging.Logger) *OpenAPIPlugin {
	return &OpenAPIPlugin{
		name:     "openapi",
		priority: 0, // Documentation, runs at the end or in Rewrite phase
		timeout:  1 * time.Second,
		failure:  engine.FailureModeFailOpen,
		logger:   logger,
	}
}

func (p *OpenAPIPlugin) Name() string { return p.name }

func (p *OpenAPIPlugin) Phase() engine.Phase {
	return engine.PhaseRewrite // Catch doc requests early
}

func (p *OpenAPIPlugin) Priority() int {
	return p.priority
}

func (p *OpenAPIPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *OpenAPIPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *OpenAPIPlugin) Initialize(config map[string]interface{}) error {
	enabled := true
	if v, ok := config["enabled"].(bool); ok {
		enabled = v
	}
	p.enabled = enabled
	if !enabled {
		return nil
	}

	pathVal, ok := config["spec_path"].(string)
	if !ok || pathVal == "" {
		return nil // no spec path, plugin is a no-op
	}
	p.specPath = pathVal
	data, err := os.ReadFile(pathVal)
	if err != nil {
		return err
	}
	p.specData = data
	p.servePath = "/openapi"
	if v, ok := config["path"].(string); ok && v != "" {
		p.servePath = v
	}
	p.format = "yaml"
	if v, ok := config["format"].(string); ok && (v == "yaml" || v == "json") {
		p.format = v
	}
	p.swaggerUI = false
	if v, ok := config["swagger_ui"].(bool); ok {
		p.swaggerUI = v
	}

	p.logger.Info("OpenAPI plugin initialized",
		logging.String("spec_path", p.specPath),
		logging.String("serve_path", p.servePath),
	)
	return nil
}

func (p *OpenAPIPlugin) Execute(ctx *engine.ExecutionContext) error {
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

	if r.URL.Path == p.servePath {
		if p.format == "json" {
			var y interface{}
			err := yaml.Unmarshal(p.specData, &y)
			if err != nil {
				return fmt.Errorf("failed to parse OpenAPI YAML: %w", err)
			}
			j, err := json.MarshalIndent(y, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to convert OpenAPI to JSON: %w", err)
			}
			w.Header().Set("Content-Type", "application/json; charset=UTF-8")
			w.WriteHeader(http.StatusOK)
			w.Write(j)
			ctx.SetState("response_written", true)
			return nil
		}
		w.Header().Set("Content-Type", "application/yaml; charset=UTF-8")
		w.WriteHeader(http.StatusOK)
		w.Write(p.specData)
		ctx.SetState("response_written", true)
		return nil
	}
	if p.swaggerUI && (r.URL.Path == "/swagger-ui/" || strings.HasPrefix(r.URL.Path, "/swagger-ui/") || r.URL.Path == "/swagger-ui") {
		if r.URL.Path == "/swagger-ui/" || r.URL.Path == "/swagger-ui" {
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(swaggerUIHTML(p.servePath, p.format)))
			ctx.SetState("response_written", true)
			return nil
		}
		// Serve static assets from embedded FS
		assetPath := strings.TrimPrefix(r.URL.Path, "/swagger-ui/")
		if assetPath == "" {
			assetPath = "index.html"
		}
		f, err := swaggerUIFS.Open(path.Join("swaggerui", assetPath))
		if err != nil {
			return fmt.Errorf("swagger UI asset not found: %w", err)
		}
		defer f.Close()
		stat, _ := f.Stat()
		http.ServeContent(w, r, assetPath, stat.ModTime(), f.(io.ReadSeeker))
		ctx.SetState("response_written", true)
		return nil
	}

	return nil
}

func swaggerUIHTML(specPath, format string) string {
	return `<!DOCTYPE html>
<html>
<head>
  <title>Swagger UI</title>
  <link rel="stylesheet" href="/swagger-ui/swagger-ui.css" />
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="/swagger-ui/swagger-ui-bundle.js"></script>
  <script src="/swagger-ui/swagger-ui-standalone-preset.js"></script>
  <script>
    window.onload = function() {
      window.ui = SwaggerUIBundle({
        url: '` + specPath + `?format=` + format + `',
        dom_id: '#swagger-ui',
        presets: [
          SwaggerUIBundle.presets.apis,
          SwaggerUIStandalonePreset
        ],
        layout: "StandaloneLayout"
      });
    };
  </script>
</body>
</html>`
}
