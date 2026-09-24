package persistence

import (
	"database/sql"
	"fmt"
)

// GetActiveModelSelection returns the model filename an admin picked for role (see migrations/
// 011) from the Settings page. ok is false and filename "" when nothing has ever been
// selected for that role — the normal state before anyone has used the picker, not an error;
// the composition root falls back to MODEL_PATH/RECOVERY_MODEL_PATH in that case.
func (t *TimescaleDB) GetActiveModelSelection(role string) (filename string, ok bool, err error) {
	ctx, cancel := t.context()
	defer cancel()

	err = t.db.QueryRowxContext(ctx,
		`SELECT model_filename FROM ml_model_selection WHERE role = $1`, role).Scan(&filename)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get active model selection for role %s: %w", role, err)
	}
	return filename, true, nil
}

// SetActiveModelSelection durably records which model filename is active for role, so the
// choice survives a restart — called only after the composition root has already loaded the
// file successfully (see app.activateModel), never speculatively.
func (t *TimescaleDB) SetActiveModelSelection(role, filename string) error {
	ctx, cancel := t.context()
	defer cancel()

	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO ml_model_selection (role, model_filename, activated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (role) DO UPDATE SET
			model_filename = EXCLUDED.model_filename,
			activated_at = EXCLUDED.activated_at`,
		role, filename); err != nil {
		return fmt.Errorf("set active model selection for role %s: %w", role, err)
	}
	return nil
}
