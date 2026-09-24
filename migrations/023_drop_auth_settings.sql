-- Atlas no longer has a login: it is a local, single-operator tool reachable on loopback only.
-- auth_settings (migrations/008) held the TOTP secret for the removed two-factor login, so it is
-- dropped rather than left behind as an unused copy of a credential. Backups taken before this
-- migration still restore; their auth_settings rows are ignored (see persistence.backupVersion).
DROP TABLE IF EXISTS auth_settings;

INSERT INTO schema_migrations (version) VALUES ('023') ON CONFLICT (version) DO NOTHING;
