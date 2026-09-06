package persistence

import (
	"context"
	"fmt"
	"strings"
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
	ctx, cancel := t.context()
	defer cancel()

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
		// UTC, not the connection's local zone: the same instant would otherwise be exported as a
		// different string depending on the server's timezone, so an export/import round trip
		// between two hosts would not be byte-stable.
		items = append(items, watchlist.Item{Symbol: row.Ticker, AddedAt: row.AddedAt.UTC().Format(time.RFC3339)})
	}
	return items, nil
}

// SaveItem idempotently adds a symbol to the watchlist: re-adding an already-tracked symbol
// is a no-op at the database level too, matching UseCase.Add's in-memory dedup.
func (t *TimescaleDB) SaveItem(item watchlist.Item) error {
	ctx, cancel := t.context()
	defer cancel()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	watchlistID, err := t.defaultWatchlistID(ctx, tx)
	if err != nil {
		return err
	}
	if err := lockWatchlist(ctx, tx, watchlistID); err != nil {
		return err
	}
	symbolID, err := upsertSymbol(ctx, tx, item.Symbol)
	if err != nil {
		return err
	}

	// added_at comes from the item, not the column default: an import carries the timestamp the
	// symbol was originally tracked from, and dropping it made a database-backed round trip
	// silently reset every symbol's history to "now" — which the in-memory tests could not see.
	// An empty AddedAt still falls back to the default, for callers that have no opinion.
	addedAt, err := parseWatchlistTime(item.AddedAt)
	if err != nil {
		return fmt.Errorf("watchlist item %s: %w", item.Symbol, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO watchlist_items (watchlist_id, symbol_id, added_at)
		 VALUES ($1, $2, COALESCE($3, now()))
		 ON CONFLICT (watchlist_id, symbol_id) DO NOTHING`,
		watchlistID, symbolID, addedAt); err != nil {
		return fmt.Errorf("insert watchlist item for %s: %w", item.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// DeleteItem removes a symbol from the default watchlist (see watchlist.UseCase.Remove).
// Removing an untracked symbol is a no-op — DELETE simply affects zero rows.
//
// Takes the same parent-row lock every other mutation takes, and for the same reason: an import
// plans against the contents it read inside its transaction, and a delete that skipped the lock
// could land between that read and the writes, so the import would faithfully restore a symbol
// the user had just removed.
func (t *TimescaleDB) DeleteItem(symbol string) error {
	ctx, cancel := t.context()
	defer cancel()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	watchlistID, err := t.defaultWatchlistID(ctx, tx)
	if err != nil {
		return err
	}
	if err := lockWatchlist(ctx, tx, watchlistID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM watchlist_items wi
		USING symbols s
		WHERE wi.symbol_id = s.id AND wi.watchlist_id = $1 AND s.ticker = $2`,
		watchlistID, symbol); err != nil {
		return fmt.Errorf("delete watchlist item for %s: %w", symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// parseWatchlistTime turns the AddedAt string the use case carries into a value Postgres can
// store, or nil for "use the column default". Rejecting an unparseable one rather than silently
// substituting now() is the point: a corrupt import should fail, not quietly rewrite history.
func parseWatchlistTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			utc := parsed.UTC()
			return &utc, nil
		}
	}
	return nil, fmt.Errorf("added_at %q must be RFC3339 or YYYY-MM-DD", value)
}
