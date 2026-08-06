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
