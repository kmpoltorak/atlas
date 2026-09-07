package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/jmoiron/sqlx"
	"time"

	"atlas/internal/domain/portfolio"
)

// defaultPortfolioName only seeds the very first portfolio a fresh install ever gets (see
// UseCase.NewUseCaseWithRepository's bootstrap) — once it exists, callers address portfolios by
// id, never by this name again. Kept distinct from any name a user later types into "create
// portfolio" or "rename portfolio".
const defaultPortfolioName = "Main Portfolio"

// ListPortfolios returns every portfolio, oldest first — bootstrap relies on that order to pick
// "the" first portfolio when none is otherwise selected.
func (t *TimescaleDB) ListPortfolios() ([]portfolio.Portfolio, error) {
	ctx, cancel := t.context()
	defer cancel()
	var rows []struct {
		ID   string `db:"id"`
		Name string `db:"name"`
	}
	if err := t.db.SelectContext(ctx, &rows,
		`SELECT id, name FROM portfolios ORDER BY created_at ASC`); err != nil {
		return nil, fmt.Errorf("list portfolios: %w", err)
	}
	portfolios := make([]portfolio.Portfolio, 0, len(rows))
	for _, row := range rows {
		portfolios = append(portfolios, portfolio.Portfolio{ID: row.ID, Name: row.Name})
	}
	// A fresh database has no portfolios at all yet; every other method here requires one to
	// already exist (there is no per-call upsert anymore, unlike the old single-portfolio
	// defaultPortfolioID). Seeding it here, on the first-ever read, keeps that invariant true
	// without every caller needing its own bootstrap logic.
	if len(portfolios) == 0 {
		created, err := t.CreatePortfolio(defaultPortfolioName)
		if err != nil {
			return nil, err
		}
		portfolios = append(portfolios, created)
	}
	return portfolios, nil
}

func (t *TimescaleDB) CreatePortfolio(name string) (portfolio.Portfolio, error) {
	ctx, cancel := t.context()
	defer cancel()
	var id string
	if err := t.db.GetContext(ctx, &id,
		`INSERT INTO portfolios (name) VALUES ($1) RETURNING id`, name); err != nil {
		return portfolio.Portfolio{}, fmt.Errorf("create portfolio %q: %w", name, err)
	}
	return portfolio.Portfolio{ID: id, Name: name}, nil
}

func (t *TimescaleDB) RenamePortfolio(id, name string) error {
	ctx, cancel := t.context()
	defer cancel()
	if _, err := t.db.ExecContext(ctx,
		`UPDATE portfolios SET name = $1 WHERE id = $2`, name, id); err != nil {
		return fmt.Errorf("rename portfolio %s: %w", id, err)
	}
	return nil
}

// DeletePortfolio removes the portfolio's positions and transactions first, in the same
// transaction — positions/transactions have no ON DELETE CASCADE on their portfolio_id FK (see
// migrations/001), so deleting the portfolio row first would fail with a foreign key violation.
func (t *TimescaleDB) DeletePortfolio(id string) error {
	ctx, cancel := t.context()
	defer cancel()
	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	if _, err := tx.ExecContext(ctx, `DELETE FROM positions WHERE portfolio_id = $1`, id); err != nil {
		return fmt.Errorf("delete positions for portfolio %s: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM transactions WHERE portfolio_id = $1`, id); err != nil {
		return fmt.Errorf("delete transactions for portfolio %s: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM portfolios WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete portfolio %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// GetPositions returns the persisted position snapshot for one portfolio.
func (t *TimescaleDB) GetPositions(portfolioID string) ([]portfolio.Position, error) {
	ctx, cancel := t.context()
	defer cancel()
	return getPositions(ctx, t.db, portfolioID)
}

// positionRow is the shared shape behind both position queries, so the single-portfolio view,
// the whole-account view and therefore the dashboard, the JSON API and the decision engine all
// resolve a position's price the same way. Splitting that resolution across call sites is what
// let the UI and the engine disagree about what a position was worth.
type positionRow struct {
	Ticker         string          `db:"ticker"`
	Quantity       float64         `db:"quantity"`
	AvgCost        float64         `db:"avg_cost"`
	TradePrice     float64         `db:"trade_price"`
	LastPrice      sql.NullFloat64 `db:"last_price"`
	QuoteUpdatedAt sql.NullTime    `db:"quote_updated_at"`
}

// positionColumns is the one place the valuation inputs are selected. Both queries below use it
// verbatim so neither can drift into pricing positions differently from the other.
const positionColumns = `s.ticker, p.quantity, p.avg_cost,
		 p.current_price AS trade_price, s.last_price, s.quote_updated_at`

// toPosition applies the valuation rule: a real quote when there is one, otherwise the last
// trade price, labelled as such. It never presents the fallback as a current market price.
func (r positionRow) toPosition() portfolio.Position {
	position := portfolio.Position{
		Symbol:      r.Ticker,
		Quantity:    r.Quantity,
		AverageCost: r.AvgCost,
		// The last recorded trade, which is a cost basis wearing a price's clothes.
		CurrentPrice: r.TradePrice,
		PriceSource:  portfolio.PriceFromTrade,
	}
	if r.LastPrice.Valid && r.LastPrice.Float64 > 0 {
		position.CurrentPrice = r.LastPrice.Float64
		position.PriceSource = portfolio.PriceFromQuote
		if r.QuoteUpdatedAt.Valid {
			position.PricedAt = r.QuoteUpdatedAt.Time.UTC()
		}
	}
	return position
}

func getPositions(ctx context.Context, q sqlx.QueryerContext, portfolioID string) ([]portfolio.Position, error) {
	var rows []positionRow
	if err := sqlx.SelectContext(ctx, q, &rows,
		`SELECT `+positionColumns+`
		 FROM positions p
		 JOIN symbols s ON s.id = p.symbol_id
		 WHERE p.portfolio_id = $1
		 ORDER BY s.ticker ASC`, portfolioID); err != nil {
		return nil, fmt.Errorf("select positions: %w", err)
	}

	positions := make([]portfolio.Position, 0, len(rows))
	for _, row := range rows {
		positions = append(positions, row.toPosition())
	}
	return positions, nil
}

// GetAllPositions aggregates every portfolio's positions by symbol — summed quantity, and a
// quantity-weighted average cost so a symbol held in two portfolios at different cost bases still
// reduces to one mathematically coherent number. CurrentPrice is taken from whichever row has the
// largest quantity (an arbitrary but stable tie-break; all portfolios' rows for a symbol converge
// on roughly the same current price anyway since it only ever reflects each portfolio's last
// recorded trade for that symbol — see applyTransaction).
func (t *TimescaleDB) GetAllPositions() ([]portfolio.Position, error) {
	ctx, cancel := t.context()
	defer cancel()

	var rows []positionRow
	if err := t.db.SelectContext(ctx, &rows,
		`SELECT `+positionColumns+`
		 FROM positions p
		 JOIN symbols s ON s.id = p.symbol_id
		 ORDER BY s.ticker ASC, p.quantity DESC`); err != nil {
		return nil, fmt.Errorf("select all positions: %w", err)
	}

	bySymbol := make(map[string]*portfolio.Position)
	order := make([]string, 0)
	for _, row := range rows {
		agg, ok := bySymbol[row.Ticker]
		if !ok {
			valued := row.toPosition()
			valued.Quantity, valued.AverageCost = 0, 0
			agg = &valued
			bySymbol[row.Ticker] = agg
			order = append(order, row.Ticker)
		}
		totalQuantity := agg.Quantity + row.Quantity
		if totalQuantity > 0 {
			agg.AverageCost = ((agg.AverageCost * agg.Quantity) + (row.AvgCost * row.Quantity)) / totalQuantity
		}
		agg.Quantity = totalQuantity
	}

	positions := make([]portfolio.Position, 0, len(order))
	for _, ticker := range order {
		positions = append(positions, *bySymbol[ticker])
	}
	return positions, nil
}

// NOT part of the application write path. Every portfolio mutation goes through MutatePortfolio
// (see portfolio_mutation.go), which commits the ledger and positions in one transaction; these
// per-row methods are deliberately absent from portfolio.Repository so no use case can reach
// them. They remain here for the constraint/round-trip tests, which need to write one row
// directly to prove the database rejects or preserves it.
//
// SavePositions idempotently upserts the full position snapshot for one portfolio: same
// (portfolio, symbol) overwrites in place, matching how the use case already recomputes the whole
// slice per trade.
func (t *TimescaleDB) SavePositions(portfolioID string, positions []portfolio.Position) error {
	ctx, cancel := t.context()
	defer cancel()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	for _, position := range positions {
		symbolID, err := upsertSymbol(ctx, tx, position.Symbol)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO positions (portfolio_id, symbol_id, quantity, avg_cost, current_price)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (portfolio_id, symbol_id) DO UPDATE SET
			   quantity = EXCLUDED.quantity, avg_cost = EXCLUDED.avg_cost,
			   current_price = EXCLUDED.current_price, updated_at = NOW()`,
			portfolioID, symbolID, position.Quantity, position.AverageCost, position.CurrentPrice); err != nil {
			return fmt.Errorf("upsert position for %s: %w", position.Symbol, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// DeletePosition removes a symbol's row from one portfolio's positions entirely (see
// portfolio.UseCase.RemovePosition) — transactions are untouched, matching the append-only
// audit-log stance the rest of this codebase takes with recommendations/predictions.
func (t *TimescaleDB) DeletePosition(portfolioID, symbol string) error {
	ctx, cancel := t.context()
	defer cancel()

	if _, err := t.db.ExecContext(ctx, `
		DELETE FROM positions
		USING symbols s
		WHERE positions.symbol_id = s.id
		  AND positions.portfolio_id = $1 AND s.ticker = $2`,
		portfolioID, symbol); err != nil {
		return fmt.Errorf("delete position for %s: %w", symbol, err)
	}
	return nil
}

// SaveTransaction appends a transaction record to one portfolio and returns its generated id (see
// migrations/003's `id` default). Transactions are append-only history, unlike positions, so
// this is a plain insert rather than an upsert.
func (t *TimescaleDB) SaveTransaction(portfolioID string, transaction portfolio.Transaction) (string, error) {
	ctx, cancel := t.context()
	defer cancel()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	symbolID, err := upsertSymbol(ctx, tx, transaction.Symbol)
	if err != nil {
		return "", err
	}

	// Timestamp arrives as a client-supplied string (see domain/portfolio.Transaction); the
	// use case doesn't validate its format, so an unparseable value falls back to now rather
	// than failing a trade record over a cosmetic field.
	timestamp, err := time.Parse(time.RFC3339, transaction.Timestamp)
	if err != nil {
		timestamp = time.Now().UTC()
	}

	// created_at is the acceptance order replay breaks ties on (migrations/021). NULL lets the
	// column default to NOW(), which is the right answer for a caller that did not set one.
	var recordedAt any
	if transaction.RecordedAt != "" {
		recordedAt = transaction.RecordedAt
	}

	var id string
	if err := tx.GetContext(ctx, &id,
		`INSERT INTO transactions (portfolio_id, symbol_id, type, quantity, price, currency, price_in_pln, timestamp, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9::timestamptz, NOW()))
		 RETURNING id`,
		portfolioID, symbolID, transaction.Type, transaction.Quantity, transaction.Price,
		transaction.Currency, transaction.PriceInPLN, timestamp, recordedAt); err != nil {
		return "", fmt.Errorf("insert transaction for %s: %w", transaction.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit transaction: %w", err)
	}
	return id, nil
}

// GetTransactions returns every persisted transaction in one portfolio across all symbols, most
// recent first — the full activity log the dashboard's transaction history table renders.
func (t *TimescaleDB) GetTransactions(portfolioID string) ([]portfolio.Transaction, error) {
	ctx, cancel := t.context()
	defer cancel()
	return getTransactions(ctx, t.db, portfolioID)
}

func getTransactions(ctx context.Context, q sqlx.QueryerContext, portfolioID string) ([]portfolio.Transaction, error) {

	var rows []struct {
		ID         string    `db:"id"`
		Ticker     string    `db:"ticker"`
		Type       string    `db:"type"`
		Quantity   float64   `db:"quantity"`
		Price      float64   `db:"price"`
		Currency   string    `db:"currency"`
		PriceInPLN float64   `db:"price_in_pln"`
		Timestamp  time.Time `db:"timestamp"`
		CreatedAt  time.Time `db:"created_at"`
	}
	// Ordered on all three keys, not just the trade date: rows sharing a timestamp used to come
	// back in whatever order the database chose, which is exactly the order replay depends on.
	if err := sqlx.SelectContext(ctx, q, &rows,
		`SELECT tr.id, s.ticker, tr.type, tr.quantity, tr.price, tr.currency, tr.price_in_pln, tr.timestamp, tr.created_at
		 FROM transactions tr
		 JOIN symbols s ON s.id = tr.symbol_id
		 WHERE tr.portfolio_id = $1
		 ORDER BY tr.timestamp DESC, tr.created_at DESC, tr.id DESC`, portfolioID); err != nil {
		return nil, fmt.Errorf("select transactions: %w", err)
	}

	transactions := make([]portfolio.Transaction, 0, len(rows))
	for _, row := range rows {
		transactions = append(transactions, portfolio.Transaction{
			ID: row.ID, Symbol: row.Ticker, Type: row.Type,
			Quantity: row.Quantity, Price: row.Price,
			Currency: row.Currency, PriceInPLN: row.PriceInPLN,
			// RFC3339Nano, not RFC3339: the column keeps sub-second precision and truncating it
			// on the way out manufactured ties between trades that had none.
			Timestamp:  row.Timestamp.UTC().Format(time.RFC3339Nano),
			RecordedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return transactions, nil
}

// DeleteTransaction removes one transaction by id (see portfolio.UseCase.RemoveTransaction).
// Unlike DeletePosition, this does touch the append-only history — undoing a mistaken entry is
// exactly what this is for; the caller is responsible for recomputing the affected position
// afterward. Not scoped to a portfolioID: the id is already globally unique (see migrations/003),
// and the caller already knows which portfolio it came from.
func (t *TimescaleDB) DeleteTransaction(id string) error {
	ctx, cancel := t.context()
	defer cancel()
	if _, err := t.db.ExecContext(ctx, `DELETE FROM transactions WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete transaction %s: %w", id, err)
	}
	return nil
}
