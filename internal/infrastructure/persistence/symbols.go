package persistence

import (
	"context"
	"fmt"
)

// sqlxGetter is satisfied by both *sqlx.DB and *sqlx.Tx, so upsertSymbol works whether or not
// the caller is inside a transaction.
type sqlxGetter interface {
	GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
}

// upsertSymbol resolves a ticker to its symbols.id, inserting it on first use. Shared by every
// repository that anchors rows to a symbol (prices, positions, watchlist_items).
func upsertSymbol(ctx context.Context, db sqlxGetter, ticker string) (string, error) {
	var symbolID string
	if err := db.GetContext(ctx, &symbolID,
		`INSERT INTO symbols (ticker) VALUES ($1)
		 ON CONFLICT (ticker) DO UPDATE SET ticker = EXCLUDED.ticker
		 RETURNING id`, ticker); err != nil {
		return "", fmt.Errorf("upsert symbol %s: %w", ticker, err)
	}
	return symbolID, nil
}

// ListSymbolsWithBars returns every ticker with at least one persisted price bar, regardless
// of current watchlist membership. Used by cmd/export-training-data to find every symbol with
// real history to train on, not just the currently-watched subset.
func (t *TimescaleDB) ListSymbolsWithBars() ([]string, error) {
	ctx := context.Background()

	var tickers []string
	if err := t.db.SelectContext(ctx, &tickers,
		`SELECT DISTINCT s.ticker
		 FROM symbols s
		 JOIN prices p ON p.symbol_id = s.id
		 ORDER BY s.ticker ASC`); err != nil {
		return nil, fmt.Errorf("list symbols with bars: %w", err)
	}
	return tickers, nil
}
