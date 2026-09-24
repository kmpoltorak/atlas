package persistence

import (
	"database/sql"
	"fmt"
)

// GetMLEnabled returns the Settings page's durable ML-blend toggle (migrations/015's
// singleton engine_settings row). ok is false when nothing has ever been toggled from the
// dashboard — the normal state before anyone has used it, not an error; the composition root
// falls back to config.Config.MLEnabled in that case.
func (t *TimescaleDB) GetMLEnabled() (enabled bool, ok bool, err error) {
	ctx, cancel := t.context()
	defer cancel()

	err = t.db.QueryRowxContext(ctx,
		`SELECT ml_enabled FROM engine_settings WHERE id = TRUE`).Scan(&enabled)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("get ml enabled setting: %w", err)
	}
	return enabled, true, nil
}

// SetMLEnabled durably records the Settings page's ML-blend toggle so it survives a restart —
// called only after the in-memory orchestrator toggle (decision.Orchestrator.SetMLEnabled) has
// already taken effect, same non-blocking-on-persistence stance as SetActiveModelSelection.
func (t *TimescaleDB) SetMLEnabled(enabled bool) error {
	ctx, cancel := t.context()
	defer cancel()

	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO engine_settings (id, ml_enabled, updated_at)
		VALUES (TRUE, $1, NOW())
		ON CONFLICT (id) DO UPDATE SET
			ml_enabled = EXCLUDED.ml_enabled,
			updated_at = NOW()`,
		enabled); err != nil {
		return fmt.Errorf("set ml enabled setting: %w", err)
	}
	return nil
}
