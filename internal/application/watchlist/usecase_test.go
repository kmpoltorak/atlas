package watchlist

import (
	"fmt"
	"testing"

	"atlas/internal/domain/watchlist"
)

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

func TestRemoveManyUntracksEachSelectedSymbol(t *testing.T) {
	uc := NewUseCase()
	_ = uc.Add("AAPL", "2026-08-05T00:00:00Z")
	_ = uc.Add("MSFT", "2026-08-05T00:00:00Z")
	_ = uc.Add("GOOG", "2026-08-05T00:00:00Z")

	if err := uc.RemoveMany([]string{"AAPL", "GOOG"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, _ := uc.List()
	if len(items) != 1 || items[0].Symbol != "MSFT" {
		t.Fatalf("expected only MSFT to remain, got %+v", items)
	}
}

func TestRemoveManyJoinsErrorsButDeletesTheRest(t *testing.T) {
	uc := NewUseCase()
	_ = uc.Add("AAPL", "2026-08-05T00:00:00Z")

	err := uc.RemoveMany([]string{"", "AAPL"})
	if err == nil {
		t.Fatal("expected an error for the empty symbol")
	}
	items, _ := uc.List()
	if len(items) != 0 {
		t.Fatalf("expected AAPL to still be removed despite the other error, got %+v", items)
	}
}

func TestRemoveManyEmptyListIsANoop(t *testing.T) {
	uc := NewUseCase()
	_ = uc.Add("AAPL", "2026-08-05T00:00:00Z")
	if err := uc.RemoveMany(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items, _ := uc.List()
	if len(items) != 1 {
		t.Fatalf("expected the existing item untouched, got %+v", items)
	}
}

func TestNormalizeSymbolIsAppliedOnEveryPath(t *testing.T) {
	// The JSON API used to pass symbols through raw while the dashboard upper-cased them, so
	// POSTing "aapl" created an entry Add's dedup would never match against "AAPL". Import made
	// that impossible to live with; this pins the fix.
	uc := NewUseCase()
	for _, raw := range []string{"aapl", " AAPL ", "Aapl"} {
		if err := uc.Add(raw, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatalf("Add(%q): %v", raw, err)
		}
	}
	if len(uc.Items) != 1 {
		t.Fatalf("expected the three spellings to collapse into one item, got %+v", uc.Items)
	}
	if uc.Items[0].Symbol != "AAPL" {
		t.Errorf("expected canonical AAPL, got %q", uc.Items[0].Symbol)
	}
	if err := uc.Remove(" aapl "); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(uc.Items) != 0 {
		t.Errorf("Remove must normalise too, still holding %+v", uc.Items)
	}
}

func TestImportMergeIsIdempotent(t *testing.T) {
	uc := NewUseCase()
	items := []watchlist.Item{{Symbol: "AAPL"}, {Symbol: "MSFT"}}

	first, err := uc.Import(items, ImportMerge, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.Added != 2 || first.Skipped != 0 || first.Removed != 0 {
		t.Fatalf("first import = %+v, want 2 added", first)
	}

	second, err := uc.Import(items, ImportMerge, "2026-02-02T00:00:00Z")
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.Added != 0 || second.Skipped != 2 || second.Removed != 0 {
		t.Errorf("re-importing the same file must change nothing, got %+v", second)
	}
	if len(uc.Items) != 2 {
		t.Errorf("expected 2 items, got %+v", uc.Items)
	}
}

func TestImportMergeNeverRemoves(t *testing.T) {
	uc := NewUseCase()
	_ = uc.Add("GLD", "2026-01-01T00:00:00Z")

	result, err := uc.Import([]watchlist.Item{{Symbol: "AAPL"}}, ImportMerge, "2026-02-02T00:00:00Z")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.Removed != 0 {
		t.Errorf("merge must not remove anything, got %+v", result)
	}
	if len(uc.Items) != 2 {
		t.Errorf("expected GLD to survive a merge, got %+v", uc.Items)
	}
}

func TestImportReplaceMirrorsTheFileAndPreservesExistingAddedAt(t *testing.T) {
	uc := NewUseCase()
	_ = uc.Add("GLD", "2026-01-01T00:00:00Z")  // absent from the import -> must go
	_ = uc.Add("AAPL", "2026-01-01T00:00:00Z") // present -> must keep its original date

	result, err := uc.Import([]watchlist.Item{
		{Symbol: "AAPL", AddedAt: "2099-12-31T00:00:00Z"},
		{Symbol: "MSFT", AddedAt: "2026-03-03T00:00:00Z"},
	}, ImportReplace, "2026-02-02T00:00:00Z")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.Added != 1 || result.Skipped != 1 || result.Removed != 1 {
		t.Fatalf("result = %+v, want 1 added / 1 skipped / 1 removed", result)
	}

	got := map[string]string{}
	for _, item := range uc.Items {
		got[item.Symbol] = item.AddedAt
	}
	if _, present := got["GLD"]; present {
		t.Error("replace must untrack symbols the file omits")
	}
	if got["AAPL"] != "2026-01-01T00:00:00Z" {
		t.Errorf("an already-tracked symbol must keep its original AddedAt, got %q", got["AAPL"])
	}
	if got["MSFT"] != "2026-03-03T00:00:00Z" {
		t.Errorf("a new symbol must take the AddedAt from the file, got %q", got["MSFT"])
	}
}

func TestImportStampsNowWhenTheFileOmitsAddedAt(t *testing.T) {
	uc := NewUseCase()
	if _, err := uc.Import([]watchlist.Item{{Symbol: "AAPL"}}, ImportMerge, "2026-05-05T00:00:00Z"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if uc.Items[0].AddedAt != "2026-05-05T00:00:00Z" {
		t.Errorf("expected the supplied now, got %q", uc.Items[0].AddedAt)
	}
}

// Repository has no transaction spanning items, so a batch that fails midway would leave the
// watchlist neither in its old state nor its new one. Bad input must therefore be rejected
// before anything is written.
func TestImportRejectsBadBatchesWithoutMutatingAnything(t *testing.T) {
	for name, items := range map[string][]watchlist.Item{
		"empty symbol": {{Symbol: "AAPL"}, {Symbol: "   "}},
		"duplicate":    {{Symbol: "AAPL"}, {Symbol: "aapl"}},
	} {
		t.Run(name, func(t *testing.T) {
			uc := NewUseCase()
			_ = uc.Add("GLD", "2026-01-01T00:00:00Z")

			if _, err := uc.Import(items, ImportReplace, "2026-02-02T00:00:00Z"); err == nil {
				t.Fatal("expected the batch to be rejected")
			}
			if len(uc.Items) != 1 || uc.Items[0].Symbol != "GLD" {
				t.Errorf("watchlist was mutated by a rejected import: %+v", uc.Items)
			}
		})
	}
}

func TestImportRejectsOversizedBatches(t *testing.T) {
	uc := NewUseCase()
	items := make([]watchlist.Item, MaxImportItems+1)
	for i := range items {
		items[i] = watchlist.Item{Symbol: fmt.Sprintf("SYM%d", i)}
	}
	if _, err := uc.Import(items, ImportMerge, "2026-01-01T00:00:00Z"); err == nil {
		t.Fatal("expected an oversized import to be rejected")
	}
	if len(uc.Items) != 0 {
		t.Errorf("oversized import must not partially apply, got %d items", len(uc.Items))
	}
}

func TestParseImportModeDefaultsToTheSafeMode(t *testing.T) {
	for value, want := range map[string]ImportMode{
		"": ImportMerge, "merge": ImportMerge, "replace": ImportReplace, "REPLACE": ImportReplace, " Merge ": ImportMerge,
	} {
		got, err := ParseImportMode(value)
		if err != nil {
			t.Fatalf("ParseImportMode(%q): %v", value, err)
		}
		if got != want {
			t.Errorf("ParseImportMode(%q) = %q, want %q", value, got, want)
		}
	}
	if _, err := ParseImportMode("wipe"); err == nil {
		t.Error("an unrecognised mode must be rejected, never coerced to replace")
	}
}

// The export shape and the import shape have to be the same one, or "move a watchlist between
// instances" needs a conversion step and stops being the feature that was asked for.
func TestExportImportRoundTrip(t *testing.T) {
	source := NewUseCase()
	_ = source.Add("AAPL", "2026-01-01T00:00:00Z")
	_ = source.Add("MSFT", "2026-01-02T00:00:00Z")
	exported, err := source.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	target := NewUseCase()
	if _, err := target.Import(exported, ImportReplace, "2026-09-09T00:00:00Z"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(target.Items) != len(source.Items) {
		t.Fatalf("round trip changed the item count: %+v vs %+v", target.Items, source.Items)
	}
	for i := range source.Items {
		if target.Items[i] != source.Items[i] {
			t.Errorf("item %d differs after round trip: %+v vs %+v", i, target.Items[i], source.Items[i])
		}
	}
}
