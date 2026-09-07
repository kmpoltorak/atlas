package persistence

import (
	"testing"
	"time"
)

// The 2026-09-06 verification's first operational gap: nothing bounded the pool, and
// database/sql's default is unlimited open connections. A caller with no opinion must still get
// a bounded pool — that is what the zero value is for.
func TestPoolLimitsFillInDefaults(t *testing.T) {
	limits := PoolLimits{}.withDefaults()

	if limits.MaxOpen != defaultMaxOpenConns || limits.MaxIdle != defaultMaxIdleConns {
		t.Fatalf("the zero value must fall back to the documented defaults, got %+v", limits)
	}
	if limits.MaxLifetime != defaultConnMaxLifetime || limits.MaxIdleTime != defaultConnMaxIdleTime {
		t.Fatalf("the zero value must bound connection age, got %+v", limits)
	}
}

func TestPoolLimitsKeepIdleBelowOpen(t *testing.T) {
	limits := PoolLimits{MaxOpen: 4, MaxIdle: 40, MaxLifetime: time.Minute}.withDefaults()

	if limits.MaxIdle != 4 {
		t.Fatalf("more idle connections than the pool may open is a typo, not a setting: got %d", limits.MaxIdle)
	}
	if limits.MaxLifetime != time.Minute {
		t.Fatalf("an explicit lifetime must survive, got %v", limits.MaxLifetime)
	}
}

// And the limits must actually reach the pool, not just the struct.
func TestNewTimescaleDBAppliesPoolLimits(t *testing.T) {
	db := connectTestDB(t)

	if got := db.db.Stats().MaxOpenConnections; got != defaultMaxOpenConns {
		t.Fatalf("expected the pool to be capped at %d connections, got %d", defaultMaxOpenConns, got)
	}
}
