package persistence

import (
	"testing"

	"atlas/internal/domain/watchlist"
)

func TestSaveItemAndGetItemsRoundTrip(t *testing.T) {
	db := connectTestDB(t)

	if err := db.SaveItem(watchlist.Item{Symbol: "TESTWATCH1"}); err != nil {
		t.Fatalf("SaveItem failed: %v", err)
	}

	items, err := db.GetItems()
	if err != nil {
		t.Fatalf("GetItems failed: %v", err)
	}
	found := false
	for _, item := range items {
		if item.Symbol == "TESTWATCH1" {
			found = true
			if item.AddedAt == "" {
				t.Fatal("expected AddedAt to be populated from the DB row")
			}
		}
	}
	if !found {
		t.Fatal("expected TESTWATCH1 to be in the watchlist")
	}
}

func TestSaveItemIsIdempotent(t *testing.T) {
	db := connectTestDB(t)

	if err := db.SaveItem(watchlist.Item{Symbol: "TESTWATCHDUP"}); err != nil {
		t.Fatalf("first SaveItem failed: %v", err)
	}
	if err := db.SaveItem(watchlist.Item{Symbol: "TESTWATCHDUP"}); err != nil {
		t.Fatalf("second SaveItem failed: %v", err)
	}

	items, err := db.GetItems()
	if err != nil {
		t.Fatalf("GetItems failed: %v", err)
	}
	count := 0
	for _, item := range items {
		if item.Symbol == "TESTWATCHDUP" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one TESTWATCHDUP entry, got %d", count)
	}
}

func TestDeleteItemRemovesTheRow(t *testing.T) {
	db := connectTestDB(t)

	if err := db.SaveItem(watchlist.Item{Symbol: "TESTDELWATCH"}); err != nil {
		t.Fatalf("SaveItem failed: %v", err)
	}
	if err := db.DeleteItem("TESTDELWATCH"); err != nil {
		t.Fatalf("DeleteItem failed: %v", err)
	}

	items, err := db.GetItems()
	if err != nil {
		t.Fatalf("GetItems failed: %v", err)
	}
	for _, item := range items {
		if item.Symbol == "TESTDELWATCH" {
			t.Fatalf("expected TESTDELWATCH to be gone, still found: %+v", item)
		}
	}
}

func TestDeleteItemForUnknownSymbolIsANoop(t *testing.T) {
	db := connectTestDB(t)
	if err := db.DeleteItem("TESTDELWATCHNEVEREXISTED"); err != nil {
		t.Fatalf("expected no error deleting a symbol that was never saved, got %v", err)
	}
}
