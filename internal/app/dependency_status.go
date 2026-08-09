package app

import "context"

// dependencyStatuses pings every service atlas-api can optionally or necessarily talk to and
// reports which are reachable. Shared by healthHandler (the JSON /health response) and
// StartDependencyStatusPolling (Prometheus gauges) so there's exactly one place that knows how
// to check each one — the two callers just interpret the same result differently (a status
// string with omitempty vs. a 0/1 gauge).
//
// A key is present only when that dependency is actually configured/applicable: "database" and
// "redis" are always checked (atlas-api can't function without either); "trainer" only when
// TRAINER_URL is set (a genuinely optional sidecar); "prometheus"/"alertmanager"/"grafana" only
// when their URL isn't configured empty (see Config.PrometheusURL's doc comment for why they
// default to non-empty in-network Compose service names, unlike TrainerURL).
func (a *Application) dependencyStatuses(ctx context.Context) map[string]bool {
	statuses := map[string]bool{
		"database": a.Storage.Ping(ctx) == nil,
		"redis":    a.Cache.Ping(ctx) == nil,
	}
	if a.Trainer != nil {
		statuses["trainer"] = a.Trainer.Ping(ctx) == nil
	}
	if a.Config.PrometheusURL != "" {
		statuses["prometheus"] = pingHTTPStatus(ctx, a.Config.PrometheusURL, "/-/healthy") == "ok"
	}
	if a.Config.AlertmanagerURL != "" {
		statuses["alertmanager"] = pingHTTPStatus(ctx, a.Config.AlertmanagerURL, "/-/healthy") == "ok"
	}
	if a.Config.GrafanaURL != "" {
		statuses["grafana"] = pingHTTPStatus(ctx, a.Config.GrafanaURL, "/api/health") == "ok"
	}
	return statuses
}
