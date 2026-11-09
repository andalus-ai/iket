package api

import "net/http"

func (api *ManagementAPI) getGatewayPolicyHits(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "policy-hit telemetry is not available in the phase-based runtime", http.StatusNotImplemented)
}

func (api *ManagementAPI) getGatewayPolicyAlerts(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "policy alert aggregation is not available in the phase-based runtime", http.StatusNotImplemented)
}

func (api *ManagementAPI) notifyGatewayPolicyAlerts(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "policy alert aggregation is not available in the phase-based runtime", http.StatusNotImplemented)
}
