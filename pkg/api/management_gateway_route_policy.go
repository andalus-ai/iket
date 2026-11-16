package api

import (
	"net/http"
	"strings"
)

func (api *ManagementAPI) getGatewayRoutePolicy(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.URL.Query().Get("path")) == "" {
		http.Error(w, "path query parameter is required", http.StatusBadRequest)
		return
	}
	http.Error(w, "route policy inspection is not available in the phase-based runtime", http.StatusNotImplemented)
}

func (api *ManagementAPI) diffGatewayRoutePolicy(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.URL.Query().Get("from_path")) == "" || strings.TrimSpace(r.URL.Query().Get("to_path")) == "" {
		http.Error(w, "from_path and to_path query parameters are required", http.StatusBadRequest)
		return
	}
	http.Error(w, "route policy comparison is not available in the phase-based runtime", http.StatusNotImplemented)
}
