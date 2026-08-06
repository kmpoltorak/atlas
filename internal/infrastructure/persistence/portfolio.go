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

// SaveTransaction appends a transaction record. Transactions are append-only history, unlike
// positions, so this is a plain insert rather than an upsert.
func (t *TimescaleDB) SaveTransaction(transaction portfolio.Transaction) error {
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
	symbolID, err := upsertSymbol(ctx, tx, transaction.Symbol)
	if err != nil {
		return err
	}

	// Timestamp arrives as a client-supplied string (see domain/portfolio.Transaction); the
	// use case doesn't validate its format, so an unparseable value falls back to now rather
	// than failing a trade record over a cosmetic field.
	timestamp, err := time.Parse(time.RFC3339, transaction.Timestamp)
	if err != nil {
		timestamp = time.Now().UTC()
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO transactions (portfolio_id, symbol_id, type, quantity, price, timestamp)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		portfolioID, symbolID, transaction.Type, transaction.Quantity, transaction.Price, timestamp); err != nil {
		return fmt.Errorf("insert transaction for %s: %w", transaction.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
