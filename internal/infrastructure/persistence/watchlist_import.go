package persistence

import (
	"context"
	"fmt"

	"atlas/internal/domain/watchlist"

	"github.com/jmoiron/sqlx"
)

// ReplaceWatchlist applies a whole import in one transaction: it locks the watchlist row, reads
// its current contents, hands them to plan, and writes the result. Either the entire import lands
// or none of it does.
//
// The 2026-09-06 review's point was that reading fresh before planning is not the same as being
// transactional. Every add and delete committed on its own, so a failure partway left a partial
// replacement, and a symbol added concurrently between the read and the writes escaped the
// replacement entirely. The row lock closes both: concurrent importers serialize on it, and a
// concurrent single add blocks until this finishes.
//
// plan receives the current items and returns the complete desired set. It must be pure — it runs
// inside the transaction, and any I/O it did would be holding a database lock.
func (t *TimescaleDB) ReplaceWatchlist(plan func(current []watchlist.Item) ([]watchlist.Item, error)) ([]watchlist.Item, error) {
	ctx, cancel := t.context()
	defer cancel()
	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin watchlist replace: %w", err)
	}
	defer tx.Rollback()

	// The lock every watchlist mutation must take. Locking the parent row rather than the item
	// rows is what makes an insert of a *new* symbol serialize too — there is no row to lock for
	// something that does not exist yet.
	//
	// Created if absent, not merely selected: neither the migrations nor GetItems ever create this
	// row — only SaveItem does — so a plain SELECT made importing into a fresh installation, or
	// into one just flushed, fail with "no rows in result set". Importing is a perfectly ordinary
	// first thing to do with an empty Atlas.
	watchlistID, err := t.defaultWatchlistID(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := lockWatchlist(ctx, tx, watchlistID); err != nil {
		return nil, err
	}

	var rows []struct {
		Ticker  string `db:"ticker"`
		AddedAt string `db:"added_at"`
	}
	if err := tx.SelectContext(ctx, &rows, `
		SELECT s.ticker, to_char(wi.added_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS added_at
		  FROM watchlist_items wi
		  JOIN symbols s ON s.id = wi.symbol_id
		 WHERE wi.watchlist_id = $1
		 ORDER BY wi.added_at ASC`, watchlistID); err != nil {
		return nil, fmt.Errorf("read watchlist for replace: %w", err)
	}
	current := make([]watchlist.Item, 0, len(rows))
	for _, row := range rows {
		current = append(current, watchlist.Item{Symbol: row.Ticker, AddedAt: row.AddedAt})
	}

	desired, err := plan(current)
	if err != nil {
		return nil, err
	}

	keep := make(map[string]bool, len(desired))
	for _, item := range desired {
		keep[item.Symbol] = true
	}
	for _, item := range current {
		if keep[item.Symbol] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM watchlist_items wi
			 USING symbols s
			 WHERE wi.symbol_id = s.id AND wi.watchlist_id = $1 AND s.ticker = $2`,
			watchlistID, item.Symbol); err != nil {
			return nil, fmt.Errorf("remove %s during import: %w", item.Symbol, err)
		}
	}
	for _, item := range desired {
		symbolID, err := upsertSymbol(ctx, tx, item.Symbol)
		if err != nil {
			return nil, err
		}
		addedAt, err := parseWatchlistTime(item.AddedAt)
		if err != nil {
			return nil, fmt.Errorf("watchlist item %s: %w", item.Symbol, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO watchlist_items (watchlist_id, symbol_id, added_at)
			VALUES ($1, $2, COALESCE($3, now()))
			ON CONFLICT (watchlist_id, symbol_id) DO NOTHING`,
			watchlistID, symbolID, addedAt); err != nil {
			return nil, fmt.Errorf("add %s during import: %w", item.Symbol, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit watchlist replace: %w", err)
	}
	return desired, nil
}

// lockWatchlist takes the parent-row lock that serializes every watchlist mutation against every
// other one. It is a separate statement from defaultWatchlistID's upsert on purpose: the upsert
// does happen to lock the row it touches, but "this INSERT ... ON CONFLICT DO UPDATE also acts as
// a lock" is not something anyone should have to reconstruct from the mutation that broke.
func lockWatchlist(ctx context.Context, tx *sqlx.Tx, watchlistID string) error {
	var locked string
	if err := tx.GetContext(ctx, &locked,
		`SELECT id FROM watchlists WHERE id = $1 FOR UPDATE`, watchlistID); err != nil {
		return fmt.Errorf("lock watchlist: %w", err)
	}
	return nil
}
