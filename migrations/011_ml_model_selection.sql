-- Which trained model file is active for each of the two independent, optional ML roles
-- (correction/recovery — see internal/application/ml.Role), chosen from the Account dashboard
-- page rather than only MODEL_PATH/RECOVERY_MODEL_PATH env vars at startup. One row per role,
-- enforced by role itself being the primary key.
CREATE TABLE IF NOT EXISTS ml_model_selection (
    role TEXT PRIMARY KEY,
    model_filename TEXT NOT NULL,
    activated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
