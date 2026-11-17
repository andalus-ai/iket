package api

import "net/http"

func (api *ManagementAPI) getGatewayShadowReport(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "shadow route reporting is not available in the phase-based runtime", http.StatusNotImplemented)
}

func (api *ManagementAPI) getGatewayShadowEvaluation(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "shadow route evaluation is not available in the phase-based runtime", http.StatusNotImplemented)
}
