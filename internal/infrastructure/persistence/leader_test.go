package persistence

import (
	"testing"
	"time"
)

// The lease is what makes more than one replica safe, so what matters is not that it can be
// taken but that it cannot be taken twice. These run against a real database on purpose: the
// mutual exclusion is a property of the SQL statement, not of any Go code, and a fake would
// only prove the fake agrees with itself.
func TestOnlyOneHolderCanTakeTheLease(t *testing.T) {
	db := connectTestDB(t)
	releaseLease(t, db)

	acquired, until, _, err := db.AcquireLeadership("replica-a", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("first acquire must succeed, got acquired=%v err=%v", acquired, err)
	}
	if until.Before(time.Now().UTC()) {
		t.Fatalf("a fresh lease must expire in the future, got %v", until)
	}

	acquired, _, _, err = db.AcquireLeadership("replica-b", time.Minute)
	if err != nil {
		t.Fatalf("second acquire returned an error: %v", err)
	}
	if acquired {
		t.Fatal("a second replica must not take a lease that is still held")
	}
}

// Renewing extends the same lease rather than competing for it, so the leader does not lose its
// own lease by refreshing it.
func TestTheHolderCanRenewItsOwnLease(t *testing.T) {
	db := connectTestDB(t)
	releaseLease(t, db)

	if _, _, _, err := db.AcquireLeadership("replica-a", 2*time.Second); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	acquired, until, _, err := db.AcquireLeadership("replica-a", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("the holder must be able to renew, got acquired=%v err=%v", acquired, err)
	}
	if time.Until(until) < 30*time.Second {
		t.Fatalf("renewal must extend the lease, got %v remaining", time.Until(until))
	}
}

// A process that dies holding the lease must not block the others forever — the lease expires on
// its own, with nothing to clean up by hand.
func TestAnExpiredLeaseIsTakenOver(t *testing.T) {
	db := connectTestDB(t)
	releaseLease(t, db)

	// A lease that has already expired stands in for a holder that died without releasing it.
	if _, _, _, err := db.AcquireLeadership("dead-replica", -time.Second); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	acquired, _, _, err := db.AcquireLeadership("replica-b", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("an expired lease must be takeable, got acquired=%v err=%v", acquired, err)
	}

	holder, _, err := db.CurrentLeader()
	if err != nil {
		t.Fatalf("read current leader: %v", err)
	}
	if holder != "replica-b" {
		t.Fatalf("expected replica-b to hold the lease, got %q", holder)
	}
}

// A clean shutdown hands over in seconds rather than after a full TTL of nobody polling.
func TestReleaseMakesTheLeaseImmediatelyAvailable(t *testing.T) {
	db := connectTestDB(t)
	releaseLease(t, db)

	if _, _, _, err := db.AcquireLeadership("replica-a", time.Hour); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := db.ReleaseLeadership("replica-a"); err != nil {
		t.Fatalf("release: %v", err)
	}

	holder, _, err := db.CurrentLeader()
	if err != nil {
		t.Fatalf("read current leader: %v", err)
	}
	if holder != "" {
		t.Fatalf("expected no leader after release, got %q", holder)
	}
	acquired, _, _, err := db.AcquireLeadership("replica-b", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("a released lease must be immediately takeable, got acquired=%v err=%v", acquired, err)
	}
}

func TestAcquireLeadershipRequiresAHolderID(t *testing.T) {
	db := connectTestDB(t)
	if _, _, _, err := db.AcquireLeadership("", time.Minute); err == nil {
		t.Fatal("an anonymous holder cannot be distinguished from another process; expected an error")
	}
}

// releaseLease clears whatever the previous test left behind, so each starts from "nobody is
// leading" regardless of order.
func releaseLease(t *testing.T, db *TimescaleDB) {
	t.Helper()
	ctx, cancel := dbContext()
	defer cancel()
	if _, err := db.db.ExecContext(ctx, `DELETE FROM leader_election`); err != nil {
		t.Fatalf("clear leader lease: %v", err)
	}
}
