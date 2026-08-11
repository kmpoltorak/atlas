package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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

// SymbolInfo is one symbols row's display/quote columns for a ticker — see UpsertSymbolInfo/
// UpsertSymbolQuote/SymbolInfoByTicker. AssetClass holds the provider's instrument type (e.g.
// "EQUITY", "ETF", "FUTURE") verbatim; it is not one of Atlas's own domain concepts.
type SymbolInfo struct {
	Name       string
	Exchange   string
	AssetClass string
	// LastPrice/PreviousClose/QuoteUpdatedAt are refreshed far more often than the fields above
	// (every alert-poll cycle, not once — see UpsertSymbolQuote); QuoteUpdatedAt is the zero
	// time until the first poll after this feature shipped has run for this symbol.
	LastPrice      float64
	PreviousClose  float64
	QuoteUpdatedAt time.Time
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

// UpsertSymbolQuote durably records a symbol's current live price and prior close (see
// market.Quote) — called once per alert-poll cycle for every watchlist symbol (see
// internal/app/alerts_loop.go), unlike UpsertSymbolInfo above which runs once per symbol ever.
// A zero Price (provider returned nothing usable) is skipped entirely rather than overwriting a
// real previous reading with a blank one — the Watchlist page keeps showing the last good quote
// until the next successful poll, not a blank cell.
func (t *TimescaleDB) UpsertSymbolQuote(ticker string, quote market.Quote) error {
	if quote.Price <= 0 {
		return nil
	}
	ctx := context.Background()

	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO symbols (ticker, last_price, previous_close, quote_updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (ticker) DO UPDATE SET
			last_price = EXCLUDED.last_price, previous_close = EXCLUDED.previous_close,
			quote_updated_at = EXCLUDED.quote_updated_at`,
		ticker, quote.Price, nullIfZero(quote.PreviousClose)); err != nil {
		return fmt.Errorf("upsert symbol quote for %s: %w", ticker, err)
	}
	return nil
}

func nullIfZero(value float64) any {
	if value == 0 {
		return nil
	}
	return value
}

// SymbolInfoByTicker returns display/quote columns for every symbol that has at least one of
// them recorded (see UpsertSymbolInfo/UpsertSymbolQuote). A ticker absent from the returned map
// simply has neither fetched yet — the normal state for a symbol added before these features
// existed — not an error; the Watchlist page's "Refresh info" button backfills the former, the
// alert-poll loop backfills the latter on its own schedule.
func (t *TimescaleDB) SymbolInfoByTicker() (map[string]SymbolInfo, error) {
	ctx := context.Background()

	var rows []struct {
		Ticker         string          `db:"ticker"`
		Name           sql.NullString  `db:"name"`
		Exchange       sql.NullString  `db:"exchange"`
		AssetClass     sql.NullString  `db:"asset_class"`
		LastPrice      sql.NullFloat64 `db:"last_price"`
		PreviousClose  sql.NullFloat64 `db:"previous_close"`
		QuoteUpdatedAt sql.NullTime    `db:"quote_updated_at"`
	}
	if err := t.db.SelectContext(ctx, &rows, `
		SELECT ticker, name, exchange, asset_class, last_price, previous_close, quote_updated_at
		FROM symbols WHERE name IS NOT NULL OR last_price IS NOT NULL`); err != nil {
		return nil, fmt.Errorf("select symbol info: %w", err)
	}

	result := make(map[string]SymbolInfo, len(rows))
	for _, row := range rows {
		result[row.Ticker] = SymbolInfo{
			Name: row.Name.String, Exchange: row.Exchange.String, AssetClass: row.AssetClass.String,
			LastPrice: row.LastPrice.Float64, PreviousClose: row.PreviousClose.Float64,
			QuoteUpdatedAt: row.QuoteUpdatedAt.Time,
		}
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
