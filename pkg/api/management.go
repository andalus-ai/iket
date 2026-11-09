package api

import (
	"sync"
	"time"

	"github.com/bhangun/iket/pkg/config"
	"github.com/bhangun/iket/pkg/core/runtime"
	"github.com/bhangun/iket/pkg/logging"
	"github.com/bhangun/iket/pkg/plugin"
	"github.com/gorilla/websocket"
)

// ManagementAPI provides REST endpoints for gateway management.
type ManagementAPI struct {
	gateway  *runtime.Gateway
	logger   *logging.Logger
	registry *plugin.Registry
	security *config.SecurityConfig
	mu       sync.RWMutex

	upgrader websocket.Upgrader

	statusSubscribers  map[*websocket.Conn]bool
	metricsSubscribers map[*websocket.Conn]bool
	logsSubscribers    map[*websocket.Conn]bool
	subscriberMu       sync.RWMutex

	apiKeyGenerator      APIKeyGenerator
	clientLifecycleHooks []ClientLifecycleHook
	queueDigestNotifyMu  sync.Mutex
	slaBreachEscalationState map[string]proposalQueueSLABreachEscalationState
	lastReload time.Time
}

// NewManagementAPI creates a new management API instance.
func NewManagementAPI(gateway *runtime.Gateway, logger *logging.Logger, registry *plugin.Registry) *ManagementAPI {
	api := &ManagementAPI{
		gateway:                   gateway,
		logger:                    logger,
		registry:                  registry,
		upgrader:                  websocket.Upgrader{},
		statusSubscribers:         make(map[*websocket.Conn]bool),
		metricsSubscribers:        make(map[*websocket.Conn]bool),
		logsSubscribers:           make(map[*websocket.Conn]bool),
		apiKeyGenerator:            CryptoRandAPIKeyGenerator{},
		clientLifecycleHooks:      []ClientLifecycleHook{noopClientLifecycleHook{}},
		slaBreachEscalationState: make(map[string]proposalQueueSLABreachEscalationState),
	}

	go api.broadcastStatusUpdates()
	go api.broadcastMetricsUpdates()

	return api
}
