package persistence

import (
	"fmt"
	"time"
)

// derivedTables are the tables Atlas regenerates rather than receives: indicator snapshots,
// recommendations and predictions are all recomputed from prices on the next poll, so old rows
// are history, not source data. Prices and anything the operator typed in (portfolios,
// transactions, the watchlist) are deliberately absent — those are never pruned automatically.
var derivedTables = map[string]string{
	"indicators":      "ts",
	"recommendations": "created_at",
	"predictions":     "created_at",
}

// PruneDerivedData deletes rows in derivedTables older than maxAge and reports how many went.
// A maxAge of 0 or less keeps everything, which is the default: automatic deletion is opt-in.
//
// This matters more than it looks. The alert loop persists one recommendation per watched symbol
// per poll — including HOLD, which is most of them — so at the default 300s interval these three
// tables grow monotonically for as long as the process runs, and BackupAll materializes all of
// them in memory as one JSON document.
func (t *TimescaleDB) PruneDerivedData(maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, nil
	}
	ctx, cancel := t.context()
	defer cancel()
	cutoff := time.Now().UTC().Add(-maxAge)
	var total int64
	// Table and column names come from the map literal above, never from a request.
	for table, column := range derivedTables {
		result, err := t.db.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE %s < $1`, table, column), cutoff)
		if err != nil {
			return total, fmt.Errorf("prune %s: %w", table, err)
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("prune %s: %w", table, err)
		}
		total += deleted
	}
	return total, nil
}
