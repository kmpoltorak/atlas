-- TOTP 2FA is enabled/disabled after login through the dashboard (QR-code enrollment via an
-- authenticator app — see internal/application/auth), not a static env-var secret, so it needs
-- durable, mutable state. Singleton row enforced by a boolean primary key that can only be TRUE
-- (a common Postgres trick for "exactly one row of global settings").
CREATE TABLE IF NOT EXISTS auth_settings (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id = TRUE),
    totp_secret TEXT,
    totp_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
