package persistence

import (
	"context"
	"fmt"
	"time"
)

// IndicatorPoint is a single named indicator value at a point in time — the shape
// SaveIndicatorSnapshot writes one row per, into the indicators table (migrations/001,
// migrations/006).
type IndicatorPoint struct {
	Type  string
	Value float64
}

// SaveIndicatorSnapshot appends one row per indicator value for a symbol at a given timestamp.
// Like recommendations/predictions, this is an append-only history log — each call adds new
// rows, never upserts. All points share one symbol lookup and one transaction so a snapshot is
// written atomically (all indicators or none), not partially.
func (t *TimescaleDB) SaveIndicatorSnapshot(symbol string, ts time.Time, points []IndicatorPoint) error {
	if len(points) == 0 {
		return nil
	}
	ctx := context.Background()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	symbolID, err := upsertSymbol(ctx, tx, symbol)
	if err != nil {
		return err
	}

	for _, point := range points {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO indicators (symbol_id, ts, indicator_type, value) VALUES ($1, $2, $3, $4)`,
			symbolID, ts, point.Type, point.Value); err != nil {
			return fmt.Errorf("insert indicator %s for %s: %w", point.Type, symbol, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
