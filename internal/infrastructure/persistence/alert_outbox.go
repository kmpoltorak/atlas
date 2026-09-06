package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrClaimLost means an acknowledgement named a claim that is no longer current — the claim
// expired mid-send and somebody else has since taken the row. The result is dropped rather than
// applied, because the current claimant's state is the newer of the two.
var ErrClaimLost = errors.New("the outbox claim was taken over before the result was recorded")

// OutboxEntry is one alert awaiting delivery. Payload is the serialized notification body,
// opaque here on purpose: the outbox's job is durability and per-channel bookkeeping, not
// knowing what an alert looks like.
type OutboxEntry struct {
	ID              string
	Symbol          string
	Action          string
	Payload         []byte
	PendingChannels []string
	Attempts        int
	CreatedAt       time.Time
	// ClaimSeq identifies the claim this entry was handed out under. It goes back in with the
	// acknowledgement, which is refused unless it is still the current claim.
	ClaimSeq int64
	// ClaimExpiresAt is when this claim lapses and the row becomes takeable again. The delivery
	// loop stops sending once it passes, rather than sending on a lease it no longer holds.
	ClaimExpiresAt time.Time
}

// EnqueueAlert records an alert *before* anything tries to send it, and advances the transition
// cursor in the same transaction — the two facts have to become true together or neither does.
//
// Both halves matter, and they used to be separate problems. The outbox exists because the old
// in-memory retry map only came into being *after* a send had already been attempted and failed,
// so a process that died between deciding to notify and notifying left nothing behind. The cursor
// exists because "what did we last announce" was previously inferred from the recommendations
// table, which ordinary HTTP browsing also writes: a crash between committing a recommendation
// and enqueuing its alert left history saying BUY with nothing pending, and the restart read that
// history and suppressed the alert as unchanged.
//
// Fenced on holder/epoch: a process that has lost the lease cannot advance the cursor for a
// successor that is already polling. The write fails with ErrNotLeader rather than landing late.
//
// channels is every channel that owes this alert. An empty list means there is nothing to
// deliver to, so the row is recorded already-delivered — it still documents the transition.
func (t *TimescaleDB) EnqueueAlert(holder string, epoch int64, symbol, action string, payload []byte, channels []string) (string, error) {
	ctx, cancel := t.context()
	defer cancel()
	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin alert enqueue: %w", err)
	}
	defer tx.Rollback()

	if err := requireLeadership(ctx, tx, holder, epoch); err != nil {
		return "", err
	}

	var id string
	if err := tx.QueryRowxContext(ctx, `
		INSERT INTO alert_outbox (symbol, action, payload, pending_channels, delivered_at)
		VALUES ($1, $2, $3, $4::text, CASE WHEN $4::text = '' THEN now() ELSE NULL END)
		RETURNING id`,
		symbol, action, payload, joinChannels(channels)).Scan(&id); err != nil {
		return "", fmt.Errorf("enqueue alert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO alert_cursor (symbol, action, outbox_id, announced_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (symbol) DO UPDATE
		   SET action = EXCLUDED.action, outbox_id = EXCLUDED.outbox_id,
		       announced_at = EXCLUDED.announced_at`,
		symbol, action, id); err != nil {
		return "", fmt.Errorf("advance alert cursor: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit alert enqueue: %w", err)
	}
	return id, nil
}

// RecordSilentTransition advances the cursor for an action that produces no notification (HOLD,
// or any action while no channel is configured). Those still have to be recorded, or the next
// real change would be measured against a stale baseline and go unannounced.
func (t *TimescaleDB) RecordSilentTransition(holder string, epoch int64, symbol, action string) error {
	ctx, cancel := t.context()
	defer cancel()
	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin cursor update: %w", err)
	}
	defer tx.Rollback()
	if err := requireLeadership(ctx, tx, holder, epoch); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO alert_cursor (symbol, action, outbox_id, announced_at)
		VALUES ($1, $2, NULL, now())
		ON CONFLICT (symbol) DO UPDATE
		   SET action = EXCLUDED.action, outbox_id = NULL, announced_at = EXCLUDED.announced_at`,
		symbol, action); err != nil {
		return fmt.Errorf("advance alert cursor: %w", err)
	}
	return tx.Commit()
}

// AlertCursor returns the last announced action per symbol — the durable answer to "have we told
// the user about this yet", read fresh at the start of every poll rather than seeded once at
// startup. Reading it per poll is what makes a promoted follower correct: it inherits the real
// cursor instead of a baseline captured before it became leader.
func (t *TimescaleDB) AlertCursor() (map[string]string, error) {
	ctx, cancel := t.context()
	defer cancel()
	var rows []struct {
		Symbol string `db:"symbol"`
		Action string `db:"action"`
	}
	if err := t.db.SelectContext(ctx, &rows, `SELECT symbol, action FROM alert_cursor`); err != nil {
		return nil, fmt.Errorf("read alert cursor: %w", err)
	}
	cursor := make(map[string]string, len(rows))
	for _, row := range rows {
		cursor[row.Symbol] = row.Action
	}
	return cursor, nil
}

// ClaimPendingAlerts takes exclusive ownership of up to limit undelivered entries, oldest first,
// and returns them. Two workers cannot claim the same row: the UPDATE ... FOR UPDATE SKIP LOCKED
// subquery hands each row to exactly one of them.
//
// The claim is a lease, not a flag, so a worker that dies mid-send does not strand the row
// forever — it becomes claimable again after claimTTL. That window is also the honest statement
// of the delivery guarantee: at-least-once per channel, bounded by how long a claim survives its
// owner. See docs/deployment.md's "Delivery guarantee".
//
// Each claim bumps claim_seq, and the entry carries both that and the claim's expiry back to the
// caller: the first fences the acknowledgement (see RecordAlertDelivery), the second tells the
// delivery loop when to stop sending on a lease it no longer holds.
func (t *TimescaleDB) ClaimPendingAlerts(holder string, limit int, claimTTL time.Duration) ([]OutboxEntry, error) {
	ctx, cancel := t.context()
	defer cancel()
	var rows []struct {
		ID              string    `db:"id"`
		Symbol          string    `db:"symbol"`
		Action          string    `db:"action"`
		Payload         []byte    `db:"payload"`
		PendingChannels string    `db:"pending_channels"`
		Attempts        int       `db:"attempts"`
		CreatedAt       time.Time `db:"created_at"`
		ClaimSeq        int64     `db:"claim_seq"`
		ClaimExpiresAt  time.Time `db:"claim_expires_at"`
	}
	if err := t.db.SelectContext(ctx, &rows, `
		UPDATE alert_outbox
		   SET claimed_by = $1, claim_expires_at = now() + $3::interval, claim_seq = claim_seq + 1
		 WHERE id IN (
		       SELECT id FROM alert_outbox
		        WHERE delivered_at IS NULL
		          AND (claim_expires_at IS NULL OR claim_expires_at < now())
		        ORDER BY created_at ASC
		        LIMIT $2
		        FOR UPDATE SKIP LOCKED)
		RETURNING id, symbol, action, payload, pending_channels, attempts, created_at,
		          claim_seq, claim_expires_at`,
		holder, limit, fmt.Sprintf("%d seconds", int(claimTTL.Seconds()))); err != nil {
		return nil, fmt.Errorf("claim pending alerts: %w", err)
	}
	entries := make([]OutboxEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, OutboxEntry{
			ID: row.ID, Symbol: row.Symbol, Action: row.Action, Payload: row.Payload,
			PendingChannels: splitChannels(row.PendingChannels), Attempts: row.Attempts, CreatedAt: row.CreatedAt,
			ClaimSeq: row.ClaimSeq, ClaimExpiresAt: row.ClaimExpiresAt.UTC(),
		})
	}
	// ORDER BY inside the subquery picks *which* rows are claimed; it says nothing about the order
	// RETURNING emits them in. Sorting here is what actually makes delivery oldest-first, which is
	// the order a user reading a notification feed expects.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].CreatedAt.Before(entries[j].CreatedAt) })
	return entries, nil
}

// PendingAlerts returns undelivered entries without claiming them — for tests and status
// display, never for delivery. Delivery must go through ClaimPendingAlerts.
func (t *TimescaleDB) PendingAlerts(limit int) ([]OutboxEntry, error) {
	ctx, cancel := t.context()
	defer cancel()
	var rows []struct {
		ID              string    `db:"id"`
		Symbol          string    `db:"symbol"`
		Action          string    `db:"action"`
		Payload         []byte    `db:"payload"`
		PendingChannels string    `db:"pending_channels"`
		Attempts        int       `db:"attempts"`
		CreatedAt       time.Time `db:"created_at"`
	}
	if err := t.db.SelectContext(ctx, &rows, `
		SELECT id, symbol, action, payload, pending_channels, attempts, created_at
		  FROM alert_outbox
		 WHERE delivered_at IS NULL
		 ORDER BY created_at ASC
		 LIMIT $1`, limit); err != nil {
		return nil, fmt.Errorf("read pending alerts: %w", err)
	}
	entries := make([]OutboxEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, OutboxEntry{
			ID: row.ID, Symbol: row.Symbol, Action: row.Action, Payload: row.Payload,
			PendingChannels: splitChannels(row.PendingChannels), Attempts: row.Attempts, CreatedAt: row.CreatedAt,
		})
	}
	return entries, nil
}

// RecordAlertDelivery narrows an entry to the channels that still owe it. An empty stillPending
// marks the whole entry delivered; anything left keeps it queued with the failure recorded, so
// the next attempt retries only those channels and never re-notifies one that already succeeded.
//
// claimSeq fences the write. Without it a worker whose claim expired mid-send could clear a
// successor's claim, restore channels the successor had already delivered to, or overwrite its
// error — stale state replacing newer state, which is a different and worse failure than the
// duplicate send at-least-once already admits to. A superseded claim matches no row and comes
// back as ErrClaimLost; the row stays with whoever holds it now.
func (t *TimescaleDB) RecordAlertDelivery(id string, claimSeq int64, stillPending []string, lastErr error) error {
	ctx, cancel := t.context()
	defer cancel()
	var message *string
	if lastErr != nil {
		text := lastErr.Error()
		message = &text
	}
	result, err := t.db.ExecContext(ctx, `
		UPDATE alert_outbox
		   SET pending_channels = $2::text,
		       attempts         = attempts + 1,
		       last_error       = $3,
		       delivered_at     = CASE WHEN $2::text = '' THEN now() ELSE NULL END,
		       -- Release the claim either way: delivered rows do not need one, and a row still
		       -- pending should be immediately retryable rather than waiting out its claim.
		       claimed_by       = NULL,
		       claim_expires_at = NULL
		 WHERE id = $1 AND claim_seq = $4`, id, joinChannels(stillPending), message, claimSeq)
	if err != nil {
		return fmt.Errorf("record alert delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("record alert delivery: %w", err)
	}
	if affected == 0 {
		return ErrClaimLost
	}
	return nil
}

// PruneDeliveredAlerts drops delivered entries past maxAge. The outbox is a delivery mechanism,
// not an audit log — the recommendations table already holds the durable history of what the
// engine said, so keeping every delivered row forever would only grow a table nobody reads.
func (t *TimescaleDB) PruneDeliveredAlerts(maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		return 0, nil
	}
	ctx, cancel := t.context()
	defer cancel()
	result, err := t.db.ExecContext(ctx,
		`DELETE FROM alert_outbox WHERE delivered_at IS NOT NULL AND delivered_at < $1`,
		time.Now().UTC().Add(-maxAge))
	if err != nil {
		return 0, fmt.Errorf("prune delivered alerts: %w", err)
	}
	return result.RowsAffected()
}

// joinChannels/splitChannels are the comma encoding for pending_channels — see migrations/018
// for why a plain TEXT column rather than TEXT[].
func joinChannels(channels []string) string { return strings.Join(channels, ",") }

func splitChannels(encoded string) []string {
	if encoded == "" {
		return nil
	}
	return strings.Split(encoded, ",")
}

// MarshalAlertPayload is the one place the outbox's payload encoding lives, so enqueue and
// delivery cannot disagree about the format.
func MarshalAlertPayload(v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode alert payload: %w", err)
	}
	return payload, nil
}

// DeleteAlertState forgets everything about one symbol's notification state — its cursor row and
// any outbox entries. Used when a symbol is gone for good, and by tests to reset between cases.
func (t *TimescaleDB) DeleteAlertState(symbol string) error {
	ctx, cancel := t.context()
	defer cancel()
	if _, err := t.db.ExecContext(ctx, `DELETE FROM alert_cursor WHERE symbol = $1`, symbol); err != nil {
		return fmt.Errorf("delete alert cursor for %s: %w", symbol, err)
	}
	if _, err := t.db.ExecContext(ctx, `DELETE FROM alert_outbox WHERE symbol = $1`, symbol); err != nil {
		return fmt.Errorf("delete outbox entries for %s: %w", symbol, err)
	}
	return nil
}
