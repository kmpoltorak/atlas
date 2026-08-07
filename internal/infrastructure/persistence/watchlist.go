package persistence

import (
	"context"
	"fmt"
	"time"

	"atlas/internal/domain/watchlist"
)

// defaultWatchlistName anchors the single watchlist Atlas currently manages, mirroring the
// single-portfolio approach in portfolio.go — no multi-user model exists yet.
const defaultWatchlistName = "default"

func (t *TimescaleDB) defaultWatchlistID(ctx context.Context, db sqlxGetter) (string, error) {
	var id string
	if err := db.GetContext(ctx, &id,
		`INSERT INTO watchlists (name) VALUES ($1)
		 ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id`, defaultWatchlistName); err != nil {
		return "", fmt.Errorf("upsert default watchlist: %w", err)
	}
	return id, nil
}

// GetItems returns the persisted watchlist, oldest-added first.
func (t *TimescaleDB) GetItems() ([]watchlist.Item, error) {
	ctx := context.Background()

	var rows []struct {
		Ticker  string    `db:"ticker"`
		AddedAt time.Time `db:"added_at"`
	}
	if err := t.db.SelectContext(ctx, &rows,
		`SELECT s.ticker, wi.added_at
		 FROM watchlist_items wi
		 JOIN symbols s ON s.id = wi.symbol_id
		 JOIN watchlists w ON w.id = wi.watchlist_id
		 WHERE w.name = $1
		 ORDER BY wi.added_at ASC`, defaultWatchlistName); err != nil {
		return nil, fmt.Errorf("select watchlist items: %w", err)
	}

	items := make([]watchlist.Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, watchlist.Item{Symbol: row.Ticker, AddedAt: row.AddedAt.Format(time.RFC3339)})
	}
	return items, nil
}

// SaveItem idempotently adds a symbol to the watchlist: re-adding an already-tracked symbol
// is a no-op at the database level too, matching UseCase.Add's in-memory dedup.
func (t *TimescaleDB) SaveItem(item watchlist.Item) error {
	ctx := context.Background()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	watchlistID, err := t.defaultWatchlistID(ctx, tx)
	if err != nil {
		return err
	}
	symbolID, err := upsertSymbol(ctx, tx, item.Symbol)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO watchlist_items (watchlist_id, symbol_id)
		 VALUES ($1, $2)
		 ON CONFLICT (watchlist_id, symbol_id) DO NOTHING`,
		watchlistID, symbolID); err != nil {
		return fmt.Errorf("insert watchlist item for %s: %w", item.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// DeleteItem removes a symbol from the default watchlist (see watchlist.UseCase.Remove).
// Removing an untracked symbol is a no-op — DELETE simply affects zero rows.
func (t *TimescaleDB) DeleteItem(symbol string) error {
	ctx := context.Background()

	if _, err := t.db.ExecContext(ctx, `
		DELETE FROM watchlist_items
		USING symbols s, watchlists w
		WHERE watchlist_items.symbol_id = s.id AND watchlist_items.watchlist_id = w.id
		  AND s.ticker = $1 AND w.name = $2`,
		symbol, defaultWatchlistName); err != nil {
		return fmt.Errorf("delete watchlist item for %s: %w", symbol, err)
	}
	return nil
}
