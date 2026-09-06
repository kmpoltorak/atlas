package persistence

import (
	"database/sql"
	"testing"
	"time"

	"atlas/internal/domain/portfolio"
)

// One valuation rule, shared by the single-portfolio and whole-account queries and therefore by
// the dashboard, the JSON API and the decision engine. These assert the rule itself, which used
// to live in three places and disagree with itself.
func TestPositionRowPrefersARealQuote(t *testing.T) {
	quotedAt := time.Date(2026, 9, 4, 16, 30, 0, 0, time.UTC)
	row := positionRow{
		Ticker: "CDR.WA", Quantity: 10, AvgCost: 100, TradePrice: 100,
		LastPrice:      sql.NullFloat64{Float64: 70, Valid: true},
		QuoteUpdatedAt: sql.NullTime{Time: quotedAt, Valid: true},
	}

	position := row.toPosition()

	if position.CurrentPrice != 70 {
		t.Fatalf("expected the market quote to price the position, got %v", position.CurrentPrice)
	}
	if position.PriceSource != portfolio.PriceFromQuote || !position.PricedAt.Equal(quotedAt) {
		t.Fatalf("expected a dated quote source, got %q at %v", position.PriceSource, position.PricedAt)
	}
	if !position.IsLive(quotedAt.Add(time.Hour), portfolio.QuoteMaxAge) {
		t.Fatal("a one-hour-old quote must count as live")
	}
}

// Without a quote the position falls back to its last trade price — but says so, so nothing
// downstream mistakes a cost basis for a valuation and reports "no loss" when it means
// "no information".
func TestPositionRowLabelsTheTradePriceFallback(t *testing.T) {
	row := positionRow{Ticker: "AAPL", Quantity: 5, AvgCost: 200, TradePrice: 200}

	position := row.toPosition()

	if position.CurrentPrice != 200 || position.PriceSource != portfolio.PriceFromTrade {
		t.Fatalf("expected a labelled trade-price fallback, got %v from %q", position.CurrentPrice, position.PriceSource)
	}
	if !position.PricedAt.IsZero() {
		t.Fatalf("a fallback price has no quote time, got %v", position.PricedAt)
	}
	if position.IsLive(time.Now(), portfolio.QuoteMaxAge) {
		t.Fatal("a trade price must never count as live")
	}
}

// A zero last_price is what the schema holds for a symbol whose quote has never been collected;
// it is not a price of zero.
func TestPositionRowIgnoresAZeroQuote(t *testing.T) {
	row := positionRow{
		Ticker: "PKN.WA", Quantity: 1, AvgCost: 60, TradePrice: 60,
		LastPrice: sql.NullFloat64{Float64: 0, Valid: true},
	}

	if position := row.toPosition(); position.CurrentPrice != 60 || position.PriceSource != portfolio.PriceFromTrade {
		t.Fatalf("a zero quote must not price a position, got %v from %q", position.CurrentPrice, position.PriceSource)
	}
}

func TestPositionIsLiveExpiresWithAge(t *testing.T) {
	quotedAt := time.Date(2026, 9, 1, 16, 30, 0, 0, time.UTC)
	position := portfolio.Position{PriceSource: portfolio.PriceFromQuote, PricedAt: quotedAt}

	if !position.IsLive(quotedAt.Add(3*24*time.Hour), portfolio.QuoteMaxAge) {
		t.Fatal("a quote from three days ago is still within the long-weekend tolerance")
	}
	if position.IsLive(quotedAt.Add(5*24*time.Hour), portfolio.QuoteMaxAge) {
		t.Fatal("a five-day-old quote must not be presented as a current price")
	}
}
