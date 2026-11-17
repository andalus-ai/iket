package api

import (
	"net/http"
)

// The phase-based runtime does not yet publish the legacy gateway's detailed
// rate-limit incident aggregates. Return an explicit capability response
// rather than fabricating empty telemetry or coupling the new runtime to the
// legacy gateway implementation. These endpoints become available when the
// runtime traffic phase exposes the corresponding telemetry contract.
func (api *ManagementAPI) writeLimitTelemetryUnavailable(w http.ResponseWriter) {
	http.Error(w, "rate-limit telemetry is not available in the phase-based runtime", http.StatusNotImplemented)
}

func (api *ManagementAPI) getGatewayLimitHits(w http.ResponseWriter, r *http.Request) {
	api.writeLimitTelemetryUnavailable(w)
}

func (api *ManagementAPI) getGatewayLimitBuckets(w http.ResponseWriter, r *http.Request) {
	api.writeLimitTelemetryUnavailable(w)
}

func (api *ManagementAPI) getGatewayLimitClasses(w http.ResponseWriter, r *http.Request) {
	api.writeLimitTelemetryUnavailable(w)
}

func (api *ManagementAPI) getGatewayLimitClassAlerts(w http.ResponseWriter, r *http.Request) {
	api.writeLimitTelemetryUnavailable(w)
}

func (api *ManagementAPI) notifyGatewayLimitClassAlerts(w http.ResponseWriter, r *http.Request) {
	api.writeLimitTelemetryUnavailable(w)
}

func (api *ManagementAPI) getGatewayLimitAlerts(w http.ResponseWriter, r *http.Request) {
	api.writeLimitTelemetryUnavailable(w)
}

func (api *ManagementAPI) notifyGatewayLimitAlerts(w http.ResponseWriter, r *http.Request) {
	api.writeLimitTelemetryUnavailable(w)
}
