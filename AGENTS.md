# Agents in Iket API Gateway

## Overview

In the context of the Iket API Gateway, "agents" refer to specialized components or plugins that perform specific functions within the gateway's architecture. These agents operate as part of the plugin system and are responsible for handling various aspects of API gateway functionality such as authentication, rate limiting, traffic management, and more.

## Types of Agents

### 1. Authentication Agents

Authentication agents handle various authentication mechanisms for securing API endpoints. These agents validate credentials and tokens before allowing requests to proceed through the gateway.

#### JWT Authentication Agent
- Validates JSON Web Tokens (JWT) using HS256/RS256 algorithms
- Extracts tokens from headers, query parameters, or cookies
- Verifies claims, issuer, and audience
- Caches validated tokens for improved performance

#### Basic Auth Agent
- Handles username/password authentication
- Validates credentials against configured user lists
- Supports secure credential storage and comparison

#### Client Credential Agent
- Manages OAuth2-style client ID/secret authentication
- Validates client credentials against registered clients
- Supports confidential and public client types

### 2. Traffic Management Agents

Traffic management agents control the flow of requests through the gateway to ensure optimal performance and prevent abuse.

#### Rate Limiting Agent
- Implements token bucket algorithm for rate limiting
- Supports multiple strategies: per-IP, per-user, per-route, and global
- Provides standard rate limit headers in responses
- Includes automatic limiter cleanup and statistics

#### Circuit Breaker Agent
- Implements three-state circuit (closed/open/half-open)
- Tracks failures using sliding window mechanism
- Configurable thresholds for opening and closing circuits
- Provides statistics and manual reset capabilities

### 3. Security Agents

Security agents enforce security policies and protect against various threats.

#### OPA Policy Agent
- Integrates with Open Policy Agent (OPA) for dynamic policy enforcement
- Caches policy decisions for improved performance
- Builds flexible inputs for policy evaluation
- Incorporates user context from authentication agents

#### Request Validation Agent
- Validates request payloads against predefined schemas
- Ensures data integrity and prevents malformed requests
- Supports various validation rules and custom validators

### 4. Routing and Proxy Agents

These agents handle request routing and forwarding to backend services.

#### Upstream Proxy Agent
- Implements reverse proxy functionality with connection pooling
- Handles configurable retry mechanisms
- Manages X-Forwarded-* headers for proper client identification
- Supports path stripping and host preservation

#### Route Matching Agent
- Matches incoming requests to configured routes
- Supports wildcard and prefix routing patterns
- Evaluates route priorities and conditions
- Integrates with tag-based routing

## Agent Lifecycle

All agents in Iket follow a standardized lifecycle managed by the plugin system:

1. **Initialization**: Agents are initialized with their configuration
2. **Startup**: Agents perform any necessary startup procedures
3. **Execution**: Agents process requests as part of the execution pipeline
4. **Shutdown**: Agents clean up resources during gateway shutdown

## Configuration

Agents are configured through the main configuration file using the `plugins` section:

```yaml
plugins:
  jwt:
    enabled: true
    secret: "your-secret-key"
    issuer: "https://auth.example.com"
    audience: "api.example.com"
  
  rate_limit:
    enabled: true
    requests_per_second: 100
    burst: 200
    strategy: "per_ip"
  
  circuit_breaker:
    enabled: true
    threshold: 5
    half_open_timeout: 30
```

## Execution Phases

Agents operate within specific phases of the request processing pipeline:

- **Rewrite Phase**: URL and path manipulation
- **Auth Phase**: Authentication and identity validation
- **Policy Phase**: Authorization and policy enforcement
- **Traffic Phase**: Rate limiting and circuit breaking
- **Upstream Phase**: Proxying to backend services
- **Response Phase**: Response transformation
- **Log Phase**: Logging and metrics collection

## Developing Custom Agents

Custom agents can be developed by implementing the `Plugin` interface and registering with the plugin registry:

```go
type MyAgent struct {
    // Agent configuration fields
}

func (a *MyAgent) Execute(ctx *ExecutionContext) error {
    // Agent logic implementation
    return nil
}

func (a *MyAgent) OnStart() error {
    // Startup logic
    return nil
}

func (a *MyAgent) OnShutdown() error {
    // Cleanup logic
    return nil
}

// Register the agent
func init() {
    registry.Register(&MyAgent{})
}
```

## Best Practices

- **Isolation**: Agents should be isolated and not depend on other agents' internal state
- **Timeouts**: Implement proper timeouts to prevent hanging requests
- **Error Handling**: Define clear failure modes (deny vs fail-open)
- **Observability**: Include proper logging and metrics collection
- **Resource Management**: Clean up resources during shutdown
- **Security**: Validate all inputs and sanitize outputs

## Monitoring and Observability

Each agent contributes to the overall observability of the gateway:

- **Metrics**: Request counts, error rates, and performance metrics
- **Logs**: Structured logging for debugging and auditing
- **Traces**: Distributed tracing with request ID propagation
- **Health Checks**: Individual agent health status reporting

## Security Considerations

- Agents should validate all configuration parameters
- Sensitive information should be handled securely
- Agents should implement proper input sanitization
- Authentication agents should use secure token handling
- Communication with external services should use TLS where appropriate