-- Whether the decision engine's ML+Portfolio+Risk blend is active (see
-- decision.Orchestrator.blendMLAndPortfolio), toggled from the Account dashboard page instead of
-- only ML_ENABLED at startup (see internal/config.Config.MLEnabled) — same singleton-row pattern
-- as auth_settings (migrations/008). No row means nothing has ever been toggled from the
-- dashboard yet, the normal state before anyone has used it; the composition root then falls
-- back to ML_ENABLED.
CREATE TABLE IF NOT EXISTS engine_settings (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id = TRUE),
    ml_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO schema_migrations (version) VALUES ('015') ON CONFLICT (version) DO NOTHING;
