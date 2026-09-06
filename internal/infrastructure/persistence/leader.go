package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// AcquireLeadership tries to take or renew the single leader lease (migrations/018), returning
// whether this holder now owns it and until when.
//
// The lease is what lets more than one replica run without each of them independently polling
// the watchlist and sending its own copy of every notification. It is a *lease*, not a lock: it
// expires on its own, so a process that dies holding it does not block the others forever, and
// nothing has to be cleaned up by hand.
//
// The whole decision is one atomic statement. Read-then-write would let two processes both see
// an expired lease and both conclude they had won it; here the WHERE clause on the UPSERT is
// evaluated under the row lock the UPSERT already takes.
func (t *TimescaleDB) AcquireLeadership(holder string, ttl time.Duration) (acquired bool, until time.Time, epoch int64, err error) {
	if holder == "" {
		return false, time.Time{}, 0, fmt.Errorf("leader holder id is required")
	}
	ctx, cancel := t.context()
	defer cancel()

	err = t.db.QueryRowxContext(ctx, `
		INSERT INTO leader_election (id, holder, acquired_at, expires_at, epoch)
		VALUES (TRUE, $1, now(), now() + $2::interval, 1)
		ON CONFLICT (id) DO UPDATE
		   SET holder      = EXCLUDED.holder,
		       -- A handover bumps the epoch; a renewal keeps it. That makes the epoch a fencing
		       -- token: a write carrying an older one comes from a process that has already been
		       -- superseded, whether or not it has noticed yet.
		       epoch       = CASE WHEN leader_election.holder = EXCLUDED.holder
		                          THEN leader_election.epoch ELSE leader_election.epoch + 1 END,
		       -- Renewing keeps the original acquisition time; taking over from an expired
		       -- holder starts a new one, so acquired_at means "since when has this process
		       -- been leader" rather than "when was this row last touched".
		       acquired_at = CASE WHEN leader_election.holder = EXCLUDED.holder
		                          THEN leader_election.acquired_at ELSE now() END,
		       expires_at  = EXCLUDED.expires_at
		 WHERE leader_election.holder = EXCLUDED.holder
		    OR leader_election.expires_at < now()
		RETURNING expires_at, epoch`,
		holder, fmt.Sprintf("%d seconds", int(ttl.Seconds()))).Scan(&until, &epoch)
	if err == sql.ErrNoRows {
		// The WHERE clause rejected the update: somebody else holds an unexpired lease. Not an
		// error — it is the normal answer for every replica that is not the leader.
		return false, time.Time{}, 0, nil
	}
	if err != nil {
		return false, time.Time{}, 0, fmt.Errorf("acquire leadership: %w", err)
	}
	return true, until.UTC(), epoch, nil
}

// ErrNotLeader is returned by a fenced write attempted by a process that no longer holds the
// lease — it lost it, or its epoch has been superseded. The caller should stop, not retry: the
// work it was doing is now somebody else's.
var ErrNotLeader = errors.New("no longer the polling leader")

// requireLeadership is the fence. Called inside a transaction, it takes the lease row's lock and
// verifies this holder still owns it at this epoch, so a batch that started as leader cannot
// finish writing after being superseded. Expiry checks in Go cannot do this: they race with the
// write they are guarding, and this does not — the row lock is held until the transaction ends.
func requireLeadership(ctx context.Context, tx *sqlx.Tx, holder string, epoch int64) error {
	var current int64
	err := tx.QueryRowxContext(ctx, `
		SELECT epoch FROM leader_election
		 WHERE id = TRUE AND holder = $1 AND epoch = $2 AND expires_at > now()
		 FOR UPDATE`, holder, epoch).Scan(&current)
	if err == sql.ErrNoRows {
		return ErrNotLeader
	}
	if err != nil {
		return fmt.Errorf("verify leadership: %w", err)
	}
	return nil
}

// ReleaseLeadership gives the lease up immediately instead of letting it expire, so a clean
// shutdown hands over in seconds rather than after a full TTL of nobody polling. Best-effort by
// nature: a process that crashes cannot call this, which is exactly why the lease also expires.
func (t *TimescaleDB) ReleaseLeadership(holder string) error {
	ctx, cancel := t.context()
	defer cancel()
	if _, err := t.db.ExecContext(ctx,
		`UPDATE leader_election SET expires_at = now() WHERE holder = $1`, holder); err != nil {
		return fmt.Errorf("release leadership: %w", err)
	}
	return nil
}

// CurrentLeader reports who holds the lease and until when — read by /health and the Account
// page so "why is nothing polling" is answerable without a database session.
func (t *TimescaleDB) CurrentLeader() (holder string, until time.Time, err error) {
	ctx, cancel := t.context()
	defer cancel()
	err = t.db.QueryRowxContext(ctx,
		`SELECT holder, expires_at FROM leader_election WHERE id = TRUE AND expires_at > now()`).
		Scan(&holder, &until)
	if err == sql.ErrNoRows {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read current leader: %w", err)
	}
	return holder, until.UTC(), nil
}

// ForceReleaseLeadership drops the lease whoever holds it. Only for tests and for an operator
// recovering from a stuck lease by hand — normal handover is ReleaseLeadership (the holder gives
// it up) or expiry. Stealing a live lease from a healthy leader is exactly the split brain the
// lease exists to prevent, so this is not called from any application path.
func (t *TimescaleDB) ForceReleaseLeadership() error {
	ctx, cancel := t.context()
	defer cancel()
	if _, err := t.db.ExecContext(ctx, `UPDATE leader_election SET expires_at = now()`); err != nil {
		return fmt.Errorf("force-release leadership: %w", err)
	}
	return nil
}

// BeginBlockingLock takes an exclusive lock on one table and holds it until the returned function
// is called. Test support only, and named so: no application path should ever hold a table lock,
// and there is no way to use this that is not deliberate.
//
// It exists because proving that a cancelled HTTP request abandons its query needs a query that
// is genuinely still running when the client hangs up. Anything faster proves nothing.
func (t *TimescaleDB) BeginBlockingLock(ctx context.Context, table string) (func(), error) {
	conn, err := t.db.Connx(ctx)
	if err != nil {
		return nil, fmt.Errorf("open blocking connection: %w", err)
	}
	tx, err := conn.BeginTxx(ctx, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("begin blocking transaction: %w", err)
	}
	// Table names here come from test code, never a request; the quoting is belt and braces.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`LOCK TABLE %q IN ACCESS EXCLUSIVE MODE`, table)); err != nil {
		_ = tx.Rollback()
		conn.Close()
		return nil, fmt.Errorf("lock %s: %w", table, err)
	}
	return func() {
		_ = tx.Rollback()
		conn.Close()
	}, nil
}
