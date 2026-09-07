-- A ledger needs the order it was written in, not only the dates it describes.
--
-- transactions.timestamp is the *trade* date, and it is legitimately ambiguous: the dashboard
-- form submits a date with no time, so two trades entered for the same day are identical to the
-- second. Replay has to order them anyway — BUY 10 then SELL 5 is a valid history, SELL 5 then
-- BUY 10 is an oversell — and until now the tie was broken by transaction id, a random UUID. The
-- same two trades could therefore replay either way, and after a restart the row order the
-- database happened to return decided which.
--
-- created_at records when Atlas accepted the row, which is the order the operator actually
-- entered them in. Existing rows all take the migration's own NOW(), so they tie with each other
-- and fall back to id as before: the acceptance order for history recorded before this column
-- existed was never captured and cannot be reconstructed here. From now on it is.
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

INSERT INTO schema_migrations (version) VALUES ('021') ON CONFLICT (version) DO NOTHING;
