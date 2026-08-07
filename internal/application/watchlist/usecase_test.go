package watchlist

import "testing"

func TestAddTracksNewSymbol(t *testing.T) {
	uc := NewUseCase()
	if err := uc.Add("AAPL", "2026-08-05T00:00:00Z"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, err := uc.List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || items[0].Symbol != "AAPL" {
		t.Fatalf("expected AAPL to be tracked, got %+v", items)
	}
}

func TestAddIgnoresDuplicateSymbol(t *testing.T) {
	uc := NewUseCase()
	if err := uc.Add("AAPL", "2026-08-05T00:00:00Z"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := uc.Add("AAPL", "2026-08-05T01:00:00Z"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, _ := uc.List()
	if len(items) != 1 {
		t.Fatalf("expected duplicate symbol to be ignored, got %d items", len(items))
	}
}

func TestAddRejectsEmptySymbol(t *testing.T) {
	uc := NewUseCase()
	if err := uc.Add("", "2026-08-05T00:00:00Z"); err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

func TestRemoveUntracksSymbol(t *testing.T) {
	uc := NewUseCase()
	_ = uc.Add("AAPL", "2026-08-05T00:00:00Z")
	_ = uc.Add("MSFT", "2026-08-05T00:00:00Z")

	if err := uc.Remove("AAPL"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, _ := uc.List()
	if len(items) != 1 || items[0].Symbol != "MSFT" {
		t.Fatalf("expected only MSFT to remain, got %+v", items)
	}
}

func TestRemoveUntrackedSymbolIsANoop(t *testing.T) {
	uc := NewUseCase()
	_ = uc.Add("AAPL", "2026-08-05T00:00:00Z")

	if err := uc.Remove("MSFT"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, _ := uc.List()
	if len(items) != 1 || items[0].Symbol != "AAPL" {
		t.Fatalf("expected AAPL to remain untouched, got %+v", items)
	}
}

func TestRemoveRejectsEmptySymbol(t *testing.T) {
	uc := NewUseCase()
	if err := uc.Remove(""); err == nil {
		t.Fatal("expected error for empty symbol")
	}
}
