package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"atlas/internal/domain/portfolio"
)

// A scoped handle must stop its work when the request behind it goes away. Without this a client
// that gave up after two seconds still paid for the full ten-second query deadline, and a browser
// reloading a slow page three times left three copies of the same query running.
func TestScopedHandleCancelsWithTheRequest(t *testing.T) {
	db := connectTestDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	scoped := db.WithContext(ctx)
	cancel()

	if _, err := scoped.GetAllPositions(); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected a cancelled request to abort the query, got %v", err)
	}
}

func TestScopedPortfolioMutationCancelsWithTheRequest(t *testing.T) {
	db := connectTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := db.WithContext(ctx).MutatePortfolio("unused", func([]portfolio.Position, []portfolio.Transaction) ([]portfolio.Position, []portfolio.Transaction, error) {
		t.Fatal("cancelled mutation must not invoke its callback")
		return nil, nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled mutation, got %v", err)
	}
}

// A deadline on the request is honoured too, and is the more common shape: an HTTP server write
// timeout, or a client that hung up.
func TestScopedHandleHonoursARequestDeadline(t *testing.T) {
	db := connectTestDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	if _, err := db.WithContext(ctx).GetAllPositions(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected an expired request deadline to abort the query, got %v", err)
	}
}

// The unscoped handle is what the background paths use — the alert loop, migrations, startup —
// and it must keep working with no request behind it.
func TestUnscopedHandleStillWorks(t *testing.T) {
	db := connectTestDB(t)

	if _, err := db.GetAllPositions(); err != nil {
		t.Fatalf("the unscoped handle must work for background paths, got %v", err)
	}
}

// Scoping is a shallow copy: the connection pool is shared, so per-request scoping costs nothing
// and cannot fragment the pool.
func TestScopingSharesTheConnectionPool(t *testing.T) {
	db := connectTestDB(t)

	if db.WithContext(context.Background()).db != db.db {
		t.Fatal("a scoped handle must share the same pool, not open its own")
	}
}
