package alerts

import (
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
}

func NewUseCase(watchlist *watchlistUC.UseCase, collector *collectorUC.UseCase, orchestrator *decisionUC.Orchestrator, window int) *UseCase {
	return &UseCase{Watchlist: watchlist, Collector: collector, Orchestrator: orchestrator, Window: window}
}

// Evaluate recomputes recommendations for every watchlist symbol. A per-symbol collection failure
// (e.g. a transient provider error) does not abort the batch; it is aggregated and returned
// alongside whatever alerts were successfully produced.
func (u *UseCase) Evaluate() ([]alerts.Alert, error) {
	items, err := u.Watchlist.List()
	if err != nil {
		return nil, err
	}

	result := make([]alerts.Alert, 0)
	var collected error
	for _, item := range items {
		bars, _, err := u.Collector.Collect(item.Symbol, u.Window)
		if err != nil {
			collected = errors.Join(collected, err)
			continue
		}
		recommendation, err := u.Orchestrator.BuildRecommendationFromBars(item.Symbol, bars)
		if err != nil {
			collected = errors.Join(collected, err)
			continue
		}
		if recommendation.Action == "HOLD" {
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
