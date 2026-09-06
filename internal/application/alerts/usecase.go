package alerts

import (
	"context"
	"errors"
	"time"

	collectorUC "atlas/internal/application/collector"
	decisionUC "atlas/internal/application/decision"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/domain/alerts"
)

// UseCase evaluates the watchlist through the decision orchestrator and surfaces alerts for any
// symbol whose current recommendation calls for action (i.e. anything other than HOLD).
type UseCase struct {
	Watchlist    *watchlistUC.UseCase
	Collector    *collectorUC.UseCase
	Orchestrator *decisionUC.Orchestrator
	Window       int

	// OnProviderCall, when set, is called once per Evaluate loop iteration that actually reached
	// the live provider (a cache hit doesn't count — see collectorUC.UseCase.Collect's fromCache),
	// with "success" or "error". This is how the background alert-poll loop — the dominant real
	// source of provider traffic — feeds the same atlas_provider_requests_total metric
	// Application.collectBars already records for direct HTTP entry points; without it, every
	// provider failure the poll loop hits was only ever visible in logs, never in Prometheus/
	// Grafana. Nil-safe and optional so this package doesn't need to import internal/observability.
	OnProviderCall func(outcome string)
}

func NewUseCase(watchlist *watchlistUC.UseCase, collector *collectorUC.UseCase, orchestrator *decisionUC.Orchestrator, window int) *UseCase {
	return &UseCase{Watchlist: watchlist, Collector: collector, Orchestrator: orchestrator, Window: window}
}

// Evaluate recomputes recommendations for every watchlist symbol. A per-symbol collection failure
// (e.g. a transient provider error) does not abort the batch; it is aggregated and returned
// alongside whatever alerts were successfully produced.
func (u *UseCase) Evaluate(ctx context.Context) ([]alerts.Alert, error) {
	all, err := u.EvaluateAll(ctx)
	result := make([]alerts.Alert, 0, len(all))
	for _, item := range all {
		if item.Recommendation.Action != "HOLD" {
			result = append(result, item)
		}
	}
	return result, err
}

// EvaluateAll includes HOLD so background polling can persist every state transition.
//
// ctx is the background caller's leadership term (see app.Leader.Term). Collecting bars for a few
// dozen symbols takes as long as the provider does, which is easily longer than a lease: without
// this check a demoted leader kept fetching and scoring on behalf of a lease somebody else held.
// The check is between symbols rather than inside the provider call — one symbol's fetch is
// already bounded by the HTTP client's own timeout.
func (u *UseCase) EvaluateAll(ctx context.Context) ([]alerts.Alert, error) {
	items, err := u.Watchlist.List()
	if err != nil {
		return nil, err
	}

	result := make([]alerts.Alert, 0)
	var collected error
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(collected, err)
		}
		bars, fromCache, err := u.Collector.Collect(item.Symbol, u.Window)
		if !fromCache && u.OnProviderCall != nil {
			if err != nil {
				u.OnProviderCall("error")
			} else {
				u.OnProviderCall("success")
			}
		}
		if err != nil {
			collected = errors.Join(collected, err)
			continue
		}
		recommendation, err := u.Orchestrator.BuildRecommendationFromBars(item.Symbol, bars)
		if err != nil {
			collected = errors.Join(collected, err)
			continue
		}
		result = append(result, alerts.Alert{
			Recommendation: recommendation,
			TriggeredAt:    time.Now().UTC().Format(time.RFC3339),
			Bars:           bars,
		})
	}
	return result, collected
}
