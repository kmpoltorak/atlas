-- Tracks when a full-database backup (persistence.TimescaleDB.BackupAll, see
-- account.html's "Backup" button) was last taken — a safety gate for the "Flush all data" button:
-- no row means no backup has ever been exported, so Flush stays blocked until one is. Same
-- singleton-row pattern as engine_settings (migrations/015). Deliberately excluded from
-- backupTables (internal/infrastructure/persistence/backup.go) — this is Atlas's own bookkeeping
-- about backups, not user data, so BackupAll doesn't dump it, RestoreAll doesn't overwrite it, and
-- FlushAll doesn't wipe it (flushing doesn't erase the fact that a backup was once taken).
CREATE TABLE IF NOT EXISTS backup_log (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id = TRUE),
    last_backup_at TIMESTAMPTZ NOT NULL
);

INSERT INTO schema_migrations (version) VALUES ('017') ON CONFLICT (version) DO NOTHING;
