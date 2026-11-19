package api

import "github.com/bhangun/iket/pkg/core/gateway"

// filterLimitClassAlertSummaryBySeverity remains a pure summary transformation.
// The phase-based runtime does not yet provide the underlying alert summary;
// its management endpoints therefore return Not Implemented until runtime
// telemetry owns this capability.
func filterLimitClassAlertSummaryBySeverity(summary gateway.RouteLimitClassAlertSummary, minSeverity string) gateway.RouteLimitClassAlertSummary {
	minRank := slaBreachTierRank(minSeverity)
	if minRank <= 0 {
		return summary
	}
	filtered := gateway.RouteLimitClassAlertSummary{
		Window:         summary.Window,
		WindowSeconds:  summary.WindowSeconds,
		MinCount:       summary.MinCount,
		TopBucketClass: summary.TopBucketClass,
		BySeverity:     map[string]int{"warning": 0, "elevated": 0, "critical": 0},
	}
	for _, alert := range summary.Alerts {
		if slaBreachTierRank(alert.Severity) < minRank {
			continue
		}
		filtered.Alerts = append(filtered.Alerts, alert)
		filtered.BySeverity[alert.Severity]++
	}
	filtered.TotalAlerts = len(filtered.Alerts)
	if len(filtered.Alerts) > 0 {
		filtered.TopBucketClass = filtered.Alerts[0].BucketClass
	}
	return filtered
}
