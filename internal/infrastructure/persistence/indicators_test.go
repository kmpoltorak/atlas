package persistence

import (
	"testing"
	"time"
)

func TestSaveIndicatorSnapshotInsertsOneRowPerPoint(t *testing.T) {
	db := connectTestDB(t)

	ts := time.Now().UTC()
	points := []IndicatorPoint{
		{Type: "rsi", Value: 62.5},
		{Type: "ema", Value: 101.2},
		{Type: "bollinger_upper", Value: 110},
	}
	if err := db.SaveIndicatorSnapshot("TESTIND", ts, points); err != nil {
		t.Fatalf("SaveIndicatorSnapshot failed: %v", err)
	}

	rows, err := db.db.Queryx(`
		SELECT i.indicator_type, i.value FROM indicators i
		JOIN symbols s ON s.id = i.symbol_id
		WHERE s.ticker = $1 AND i.ts = $2
		ORDER BY i.indicator_type`, "TESTIND", ts)
	if err != nil {
		t.Fatalf("failed to read back indicators: %v", err)
	}
	defer func() { _ = rows.Close() }()

	got := map[string]float64{}
	for rows.Next() {
		var indicatorType string
		var value float64
		if err := rows.Scan(&indicatorType, &value); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		got[indicatorType] = value
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 persisted indicator rows, got %d (%+v)", len(got), got)
	}
	if got["rsi"] != 62.5 || got["ema"] != 101.2 || got["bollinger_upper"] != 110 {
		t.Fatalf("unexpected stored values: %+v", got)
	}
}

func TestSaveIndicatorSnapshotWithNoPointsIsANoop(t *testing.T) {
	db := connectTestDB(t)
	if err := db.SaveIndicatorSnapshot("TESTINDEMPTY", time.Now(), nil); err != nil {
		t.Fatalf("expected no error for an empty snapshot, got %v", err)
	}
}
