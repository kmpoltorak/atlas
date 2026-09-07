package persistence

import (
	"atlas/internal/domain/portfolio"
	"fmt"
)

// MutatePortfolio serializes writes on the parent row, then commits the ledger and
// materialized positions in one SQL transaction. It always reads the current state.
func (t *TimescaleDB) MutatePortfolio(id string, apply func([]portfolio.Position, []portfolio.Transaction) ([]portfolio.Position, []portfolio.Transaction, error)) error {
	ctx, cancel := t.context()
	defer cancel()
	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin portfolio mutation: %w", err)
	}
	defer tx.Rollback()
	var locked string
	if err := tx.GetContext(ctx, &locked, `SELECT id FROM portfolios WHERE id=$1 FOR UPDATE`, id); err != nil {
		return fmt.Errorf("lock portfolio: %w", err)
	}
	positions, err := getPositions(ctx, tx, id)
	if err != nil {
		return err
	}
	history, err := getTransactions(ctx, tx, id)
	if err != nil {
		return err
	}
	updatedPositions, updatedHistory, err := apply(positions, history)
	if err != nil {
		return err
	}
	old := make(map[string]bool, len(history))
	for _, item := range history {
		old[item.ID] = true
	}
	retained := make(map[string]bool, len(updatedHistory))
	for _, item := range updatedHistory {
		retained[item.ID] = true
		if old[item.ID] {
			continue
		}
		symbolID, err := upsertSymbol(ctx, tx, item.Symbol)
		if err != nil {
			return err
		}
		// created_at carries the acceptance order (migrations/021); NULL takes the column's
		// NOW() default, which is what a caller that never set one means.
		var recordedAt any
		if item.RecordedAt != "" {
			recordedAt = item.RecordedAt
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO transactions (id,portfolio_id,symbol_id,type,quantity,price,currency,price_in_pln,timestamp,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,COALESCE($10::timestamptz, NOW()))`, item.ID, id, symbolID, item.Type, item.Quantity, item.Price, item.Currency, item.PriceInPLN, item.Timestamp, recordedAt); err != nil {
			return fmt.Errorf("insert portfolio transaction: %w", err)
		}
	}
	for _, item := range history {
		if !retained[item.ID] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM transactions WHERE portfolio_id=$1 AND id=$2`, id, item.ID); err != nil {
				return fmt.Errorf("delete portfolio transaction: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM positions WHERE portfolio_id=$1`, id); err != nil {
		return fmt.Errorf("replace positions: %w", err)
	}
	for _, position := range updatedPositions {
		symbolID, err := upsertSymbol(ctx, tx, position.Symbol)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO positions (portfolio_id,symbol_id,quantity,avg_cost,current_price) VALUES ($1,$2,$3,$4,$5)`, id, symbolID, position.Quantity, position.AverageCost, position.CurrentPrice); err != nil {
			return fmt.Errorf("write position: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit portfolio mutation: %w", err)
	}
	return nil
}
