package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRecordNumouIntrospectionUsesBoundedOutcomes(t *testing.T) {
	collector := NewCollector()
	collector.RecordNumouIntrospection("active", 25*time.Millisecond)
	collector.RecordNumouIntrospection("inactive", 10*time.Millisecond)
	collector.RecordNumouIntrospection("arbitrary-sensitive-value", -time.Second)

	recorder := httptest.NewRecorder()
	collector.ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()

	for _, expected := range []string{
		"gateway_numou_introspection_total{outcome=\"active\"} 1",
		"gateway_numou_introspection_total{outcome=\"inactive\"} 1",
		"gateway_numou_introspection_total{outcome=\"error\"} 1",
		"gateway_numou_introspection_duration_seconds_count{outcome=\"active\"} 1",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics output missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "arbitrary-sensitive-value") {
		t.Fatalf("unbounded introspection outcome leaked into metric labels:\n%s", body)
	}
}
