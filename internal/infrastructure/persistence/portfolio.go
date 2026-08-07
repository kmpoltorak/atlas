package persistence

import (
	"context"
	"fmt"
	"time"

	"atlas/internal/domain/portfolio"
)

// defaultPortfolioName anchors the single portfolio Atlas currently manages — there is no
// multi-user/multi-portfolio model yet (see PROJECT_CONTEXT.md's Portfolio Engine scope).
const defaultPortfolioName = "default"

func (t *TimescaleDB) defaultPortfolioID(ctx context.Context, db sqlxGetter) (string, error) {
	var id string
	if err := db.GetContext(ctx, &id,
		`INSERT INTO portfolios (name) VALUES ($1)
		 ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id`, defaultPortfolioName); err != nil {
		return "", fmt.Errorf("upsert default portfolio: %w", err)
	}
	return id, nil
}

// GetPositions returns the persisted position snapshot for the default portfolio.
func (t *TimescaleDB) GetPositions() ([]portfolio.Position, error) {
	ctx := context.Background()

	var rows []struct {
		Ticker       string  `db:"ticker"`
		Quantity     float64 `db:"quantity"`
		AvgCost      float64 `db:"avg_cost"`
		CurrentPrice float64 `db:"current_price"`
	}
	if err := t.db.SelectContext(ctx, &rows,
		`SELECT s.ticker, p.quantity, p.avg_cost, p.current_price
		 FROM positions p
		 JOIN symbols s ON s.id = p.symbol_id
		 JOIN portfolios pf ON pf.id = p.portfolio_id
		 WHERE pf.name = $1
		 ORDER BY s.ticker ASC`, defaultPortfolioName); err != nil {
		return nil, fmt.Errorf("select positions: %w", err)
	}

	positions := make([]portfolio.Position, 0, len(rows))
	for _, row := range rows {
		positions = append(positions, portfolio.Position{
			Symbol: row.Ticker, Quantity: row.Quantity,
			AverageCost: row.AvgCost, CurrentPrice: row.CurrentPrice,
		})
	}
	return positions, nil
}

// SavePositions idempotently upserts the full position snapshot: same (portfolio, symbol)
// overwrites in place, matching how the use case already recomputes the whole slice per trade.
func (t *TimescaleDB) SavePositions(positions []portfolio.Position) error {
	ctx := context.Background()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	portfolioID, err := t.defaultPortfolioID(ctx, tx)
	if err != nil {
		return err
	}

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

// DeletePosition removes a symbol's row from the default portfolio's positions entirely (see
// portfolio.UseCase.RemovePosition) — transactions are untouched, matching the append-only
// audit-log stance the rest of this codebase takes with recommendations/predictions.
func (t *TimescaleDB) DeletePosition(symbol string) error {
	ctx := context.Background()

	if _, err := t.db.ExecContext(ctx, `
		DELETE FROM positions
		USING symbols s, portfolios pf
		WHERE positions.symbol_id = s.id AND positions.portfolio_id = pf.id
		  AND s.ticker = $1 AND pf.name = $2`,
		symbol, defaultPortfolioName); err != nil {
		return fmt.Errorf("delete position for %s: %w", symbol, err)
	}
	return nil
}

// SaveTransaction appends a transaction record and returns its generated id (see
// migrations/003's `id` default). Transactions are append-only history, unlike positions, so
// this is a plain insert rather than an upsert.
func (t *TimescaleDB) SaveTransaction(transaction portfolio.Transaction) (string, error) {
	ctx := context.Background()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	portfolioID, err := t.defaultPortfolioID(ctx, tx)
	if err != nil {
		return "", err
	}
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

	var id string
	if err := tx.GetContext(ctx, &id,
		`INSERT INTO transactions (portfolio_id, symbol_id, type, quantity, price, timestamp)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		portfolioID, symbolID, transaction.Type, transaction.Quantity, transaction.Price, timestamp); err != nil {
		return "", fmt.Errorf("insert transaction for %s: %w", transaction.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit transaction: %w", err)
	}
	return id, nil
}

// GetTransactions returns every persisted transaction in the default portfolio across all
// symbols, most recent first — the full activity log the dashboard's transaction history table
// renders.
func (t *TimescaleDB) GetTransactions() ([]portfolio.Transaction, error) {
	ctx := context.Background()

	var rows []struct {
		ID        string    `db:"id"`
		Ticker    string    `db:"ticker"`
		Type      string    `db:"type"`
		Quantity  float64   `db:"quantity"`
		Price     float64   `db:"price"`
		Timestamp time.Time `db:"timestamp"`
	}
	if err := t.db.SelectContext(ctx, &rows,
		`SELECT tr.id, s.ticker, tr.type, tr.quantity, tr.price, tr.timestamp
		 FROM transactions tr
		 JOIN symbols s ON s.id = tr.symbol_id
		 JOIN portfolios pf ON pf.id = tr.portfolio_id
		 WHERE pf.name = $1
		 ORDER BY tr.timestamp DESC`, defaultPortfolioName); err != nil {
		return nil, fmt.Errorf("select transactions: %w", err)
	}

	transactions := make([]portfolio.Transaction, 0, len(rows))
	for _, row := range rows {
		transactions = append(transactions, portfolio.Transaction{
			ID: row.ID, Symbol: row.Ticker, Type: row.Type,
			Quantity: row.Quantity, Price: row.Price,
			Timestamp: row.Timestamp.UTC().Format(time.RFC3339),
		})
	}
	return transactions, nil
}

// DeleteTransaction removes one transaction by id (see portfolio.UseCase.RemoveTransaction).
// Unlike DeletePosition, this does touch the append-only history — undoing a mistaken entry is
// exactly what this is for; the caller is responsible for recomputing the affected position
// afterward.
func (t *TimescaleDB) DeleteTransaction(id string) error {
	ctx := context.Background()
	if _, err := t.db.ExecContext(ctx, `DELETE FROM transactions WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete transaction %s: %w", id, err)
	}
	return nil
}
