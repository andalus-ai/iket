package graphql

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/core/engine"
	"github.com/bhangun/iket/pkg/logging"
	graph "github.com/graph-gophers/graphql-go"
	"github.com/graph-gophers/graphql-go/relay"
)

// GraphQLPlugin implements GraphQL support
type GraphQLPlugin struct {
	name        string
	priority    int
	timeout     time.Duration
	failure     engine.FailureMode
	enabled     bool
	schema      *graph.Schema
	resolvers   map[string]interface{}
	mutex       sync.RWMutex
	httpHandler http.Handler
	logger      *logging.Logger
}

// NewGraphQLPlugin creates a new GraphQL plugin
func NewGraphQLPlugin(logger *logging.Logger) *GraphQLPlugin {
	return &GraphQLPlugin{
		name:      "graphql",
		priority:  50,
		timeout:   5 * time.Second,
		failure:   engine.FailureModeDeny,
		resolvers: make(map[string]interface{}),
		logger:    logger,
	}
}

func (p *GraphQLPlugin) Name() string { return p.name }

func (p *GraphQLPlugin) Phase() engine.Phase {
	return engine.PhaseTraffic
}

func (p *GraphQLPlugin) Priority() int {
	return p.priority
}

func (p *GraphQLPlugin) Timeout() time.Duration {
	return p.timeout
}

func (p *GraphQLPlugin) OnFailure() engine.FailureMode {
	return p.failure
}

func (p *GraphQLPlugin) Initialize(config map[string]interface{}) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if enabled, ok := config["enabled"].(bool); ok {
		p.enabled = enabled
	}

	if !p.enabled {
		return nil
	}

	// Load schema from file if specified
	if schemaPath, ok := config["schema_path"].(string); ok && schemaPath != "" {
		schemaData, err := os.ReadFile(schemaPath)
		if err != nil {
			return fmt.Errorf("failed to read GraphQL schema: %w", err)
		}
		if err := p.RegisterSchema(string(schemaData)); err != nil {
			return fmt.Errorf("failed to register schema: %w", err)
		}
	}

	p.httpHandler = p.createHTTPHandler()
	p.logger.Info("GraphQL plugin initialized")
	return nil
}

func (p *GraphQLPlugin) RegisterSchema(schemaStr string) error {
	schema, err := graph.ParseSchema(schemaStr, p)
	if err != nil {
		return fmt.Errorf("failed to parse schema: %w", err)
	}

	p.schema = schema
	return nil
}

func (p *GraphQLPlugin) createHTTPHandler() http.Handler {
	if p.schema == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "GraphQL schema not initialized", http.StatusInternalServerError)
		})
	}

	return &relay.Handler{Schema: p.schema}
}

func (p *GraphQLPlugin) Execute(ctx *engine.ExecutionContext) error {
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

	// Handle GraphQL requests
	if r.URL.Path == "/graphql" {
		p.httpHandler.ServeHTTP(w, r)
		ctx.SetState("response_written", true)
		return nil
	}

	return nil
}

// Query implements the Resolver interface
func (p *GraphQLPlugin) Query() interface{} {
	return p.resolvers["Query"]
}

// Mutation implements the Resolver interface
func (p *GraphQLPlugin) Mutation() interface{} {
	return p.resolvers["Mutation"]
}
