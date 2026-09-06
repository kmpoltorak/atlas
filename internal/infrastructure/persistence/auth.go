package persistence

import (
	"database/sql"
	"fmt"
)

// GetTOTP returns the current 2FA settings (migrations/008's singleton auth_settings row).
// enabled is false and secret is "" when no row exists yet — the normal state before 2FA has
// ever been enrolled, not an error.
func (t *TimescaleDB) GetTOTP() (secret string, enabled bool, err error) {
	ctx, cancel := t.context()
	defer cancel()

	var nullableSecret sql.NullString
	err = t.db.QueryRowxContext(ctx,
		`SELECT totp_secret, totp_enabled FROM auth_settings WHERE id = TRUE`).
		Scan(&nullableSecret, &enabled)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get totp settings: %w", err)
	}
	return nullableSecret.String, enabled, nil
}

// SetTOTP upserts the singleton 2FA settings row — used both to activate 2FA (secret set,
// enabled true, after QR-enrollment confirmation) and to disable it (secret cleared, enabled
// false). secret is stored as SQL NULL, not an empty string, when cleared.
func (t *TimescaleDB) SetTOTP(secret string, enabled bool) error {
	ctx, cancel := t.context()
	defer cancel()

	var secretValue any
	if secret != "" {
		secretValue = secret
	}
	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO auth_settings (id, totp_secret, totp_enabled, updated_at)
		VALUES (TRUE, $1, $2, NOW())
		ON CONFLICT (id) DO UPDATE SET
			totp_secret = EXCLUDED.totp_secret,
			totp_enabled = EXCLUDED.totp_enabled,
			updated_at = NOW()`,
		secretValue, enabled); err != nil {
		return fmt.Errorf("set totp settings: %w", err)
	}
	return nil
}
