package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"atlas/internal/domain/market"
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

// SymbolInfo is symbols.name/exchange/asset_class for one ticker — see UpsertSymbolInfo/
// SymbolInfoByTicker. AssetClass holds the provider's instrument type (e.g. "EQUITY", "ETF",
// "FUTURE") verbatim; it is not one of Atlas's own domain concepts.
type SymbolInfo struct {
	Name       string
	Exchange   string
	AssetClass string
}

// UpsertSymbolInfo durably records what a symbol actually is (see market.InstrumentInfo) —
// fetched once per symbol (watchlist add, or the Watchlist page's "Refresh info" button
// backfilling symbols added before this existed — see internal/app/web_handlers.go), not on
// every bar collection, since a ticker's name/exchange/instrument type essentially never
// changes. An empty field from the provider is stored as SQL NULL rather than "", matching
// upsertSymbol's plain ticker-only insert leaving these columns NULL until this is called.
func (t *TimescaleDB) UpsertSymbolInfo(ticker string, info market.InstrumentInfo) error {
	ctx := context.Background()

	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO symbols (ticker, name, exchange, asset_class)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (ticker) DO UPDATE SET
			name = EXCLUDED.name, exchange = EXCLUDED.exchange, asset_class = EXCLUDED.asset_class`,
		ticker, nullIfEmpty(info.Name), nullIfEmpty(info.Exchange), nullIfEmpty(info.Type)); err != nil {
		return fmt.Errorf("upsert symbol info for %s: %w", ticker, err)
	}
	return nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// SymbolInfoByTicker returns name/exchange/asset_class for every symbol that has one recorded
// (see UpsertSymbolInfo). A ticker absent from the returned map simply has none fetched yet —
// the normal state for a symbol added before this feature existed, or one the provider had no
// metadata for — not an error; the Watchlist page's "Refresh info" button is what backfills it.
func (t *TimescaleDB) SymbolInfoByTicker() (map[string]SymbolInfo, error) {
	ctx := context.Background()

	var rows []struct {
		Ticker     string         `db:"ticker"`
		Name       sql.NullString `db:"name"`
		Exchange   sql.NullString `db:"exchange"`
		AssetClass sql.NullString `db:"asset_class"`
	}
	if err := t.db.SelectContext(ctx, &rows,
		`SELECT ticker, name, exchange, asset_class FROM symbols WHERE name IS NOT NULL`); err != nil {
		return nil, fmt.Errorf("select symbol info: %w", err)
	}

	result := make(map[string]SymbolInfo, len(rows))
	for _, row := range rows {
		result[row.Ticker] = SymbolInfo{Name: row.Name.String, Exchange: row.Exchange.String, AssetClass: row.AssetClass.String}
	}
	return result, nil
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
