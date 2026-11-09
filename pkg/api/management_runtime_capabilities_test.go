package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPhaseRuntimeUnsupportedManagementCapabilitiesReturnNotImplemented(t *testing.T) {
	api := &ManagementAPI{}
	tests := []struct {
		name string
		handler http.HandlerFunc
	}{
		{name: "rate limit hits", handler: api.getGatewayLimitHits},
		{name: "rate limit buckets", handler: api.getGatewayLimitBuckets},
		{name: "rate limit classes", handler: api.getGatewayLimitClasses},
		{name: "rate limit class alerts", handler: api.getGatewayLimitClassAlerts},
		{name: "notify rate limit class alerts", handler: api.notifyGatewayLimitClassAlerts},
		{name: "rate limit alerts", handler: api.getGatewayLimitAlerts},
		{name: "notify rate limit alerts", handler: api.notifyGatewayLimitAlerts},
		{name: "policy hits", handler: api.getGatewayPolicyHits},
		{name: "policy alerts", handler: api.getGatewayPolicyAlerts},
		{name: "notify policy alerts", handler: api.notifyGatewayPolicyAlerts},
		{name: "shadow report", handler: api.getGatewayShadowReport},
		{name: "shadow evaluation", handler: api.getGatewayShadowEvaluation},
		{name: "backend status", handler: api.getGatewayBackends},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			tt.handler(recorder, request)
			if recorder.Code != http.StatusNotImplemented {
				t.Fatalf("expected status %d, got %d: %s", http.StatusNotImplemented, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestRoutePolicyManagementValidatesRequiredPathBeforeCapabilityCheck(t *testing.T) {
	api := &ManagementAPI{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/?path=", nil)

	api.getGatewayRoutePolicy(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, recorder.Code)
	}
}
