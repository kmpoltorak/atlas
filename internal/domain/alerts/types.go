package alerts

import (
	"atlas/internal/domain/decision"
	"atlas/internal/domain/market"
)

// Alert wraps a non-HOLD recommendation surfaced from evaluating the watchlist.
type Alert struct {
	Recommendation decision.Recommendation `json:"recommendation"`
	TriggeredAt    string                  `json:"triggered_at"`

	// Bars are the bars the recommendation was built from — internal-only (never serialized;
	// GET /alerts callers don't need a symbol's whole bar window echoed back), used solely so
	// callers can persist an indicator snapshot alongside the recommendation the same way
	// recommendationsHandler already does (see app.persistIndicatorSnapshot).
	Bars []market.Bar `json:"-"`
}
