package persistence

import (
	"errors"
	"fmt"
	"time"
)

// derivedTables are the tables Atlas regenerates rather than receives: indicator snapshots,
// recommendations and predictions are all recomputed from prices on the next poll, so old rows
// are history, not source data. Prices and anything the operator typed in (portfolios,
// transactions, the watchlist) are deliberately absent — those are never pruned automatically.
// The column name per table is not guessable — predictions and indicators timestamp their rows
// with ts, recommendations with created_at (migrations/001). Naming created_at for predictions
// made every prune fail with `column "created_at" does not exist`, so with retention switched on
// nothing was pruned at all and the cycle reported an error.
var derivedTables = map[string]string{
	"indicators":      "ts",
	"recommendations": "created_at",
	"predictions":     "ts",
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
	// Every table is attempted and the failures are joined, rather than the first one aborting
	// the rest: map iteration order is random, so a single broken table used to prune a different
	// arbitrary subset of the others on every run — the batch rule the rest of this codebase
	// already follows (see portfolio.UseCase.RemoveTransactions).
	var errs []error
	// Table and column names come from the map literal above, never from a request.
	for table, column := range derivedTables {
		result, err := t.db.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE %s < $1`, table, column), cutoff)
		if err != nil {
			errs = append(errs, fmt.Errorf("prune %s: %w", table, err))
			continue
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			errs = append(errs, fmt.Errorf("prune %s: %w", table, err))
			continue
		}
		total += deleted
	}
	return total, errors.Join(errs...)
}
