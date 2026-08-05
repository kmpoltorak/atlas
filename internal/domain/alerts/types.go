package alerts

import "atlas/internal/domain/decision"

// Alert wraps a non-HOLD recommendation surfaced from evaluating the watchlist.
type Alert struct {
	Recommendation decision.Recommendation `json:"recommendation"`
	TriggeredAt    string                  `json:"triggered_at"`
}
