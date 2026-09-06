package watchlist

import (
	"errors"
	"testing"

	domain "atlas/internal/domain/watchlist"
)

type sharedWatchlistRepository struct {
	items    []domain.Item
	readErr  error
	writeErr error
}

func (r *sharedWatchlistRepository) GetItems() ([]domain.Item, error) {
	return append([]domain.Item(nil), r.items...), r.readErr
}

func (r *sharedWatchlistRepository) SaveItem(item domain.Item) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	for _, existing := range r.items {
		if existing.Symbol == item.Symbol {
			return nil
		}
	}
	r.items = append(r.items, item)
	return nil
}

// ReplaceWatchlist makes this fake a TransactionalRepository. It applies the plan against its
// current contents and swaps the whole slice, which is the in-memory equivalent of the real
// implementation's single transaction: either everything lands or nothing does.
func (r *sharedWatchlistRepository) ReplaceWatchlist(
	plan func(current []domain.Item) ([]domain.Item, error),
) ([]domain.Item, error) {
	if r.readErr != nil {
		return nil, r.readErr
	}
	desired, err := plan(append([]domain.Item(nil), r.items...))
	if err != nil {
		return nil, err
	}
	if r.writeErr != nil {
		return nil, r.writeErr
	}
	r.items = desired
	return desired, nil
}

func (r *sharedWatchlistRepository) DeleteItem(symbol string) error {
	for i, item := range r.items {
		if item.Symbol == symbol {
			r.items = append(r.items[:i], r.items[i+1:]...)
			break
		}
	}
	return nil
}

func TestAddRestoresItemRemovedByAnotherReplica(t *testing.T) {
	repo := &sharedWatchlistRepository{items: []domain.Item{{Symbol: "AAPL"}}}
	a, err := NewUseCaseWithRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewUseCaseWithRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Remove("AAPL"); err != nil {
		t.Fatal(err)
	}
	if err := a.Add("aapl", ""); err != nil {
		t.Fatal(err)
	}
	if len(repo.items) != 1 || repo.items[0].Symbol != "AAPL" {
		t.Fatalf("add used stale local state: %+v", repo.items)
	}
}

func TestAddCachedItemStillReportsPersistenceFailure(t *testing.T) {
	repo := &sharedWatchlistRepository{items: []domain.Item{{Symbol: "AAPL"}}}
	u, err := NewUseCaseWithRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	repo.writeErr = errors.New("database unavailable")
	if err := u.Add("AAPL", ""); !errors.Is(err, repo.writeErr) {
		t.Fatalf("expected persistence failure, got %v", err)
	}
}

func TestReplaceImportUsesCurrentRepositoryContents(t *testing.T) {
	repo := &sharedWatchlistRepository{items: []domain.Item{{Symbol: "AAPL"}}}
	u, err := NewUseCaseWithRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	// Another replica removes AAPL and adds MSFT after this use case loads.
	repo.items = []domain.Item{{Symbol: "MSFT"}}
	result, err := u.Import([]domain.Item{{Symbol: "AAPL"}}, ImportReplace, "")
	if err != nil {
		t.Fatal(err)
	}
	if result != (ImportResult{Added: 1, Removed: 1}) {
		t.Fatalf("unexpected import counts: %+v", result)
	}
	if len(repo.items) != 1 || repo.items[0].Symbol != "AAPL" {
		t.Fatalf("replace retained stale repository contents: %+v", repo.items)
	}
}

func TestImportReadFailureDoesNotWrite(t *testing.T) {
	repo := &sharedWatchlistRepository{items: []domain.Item{{Symbol: "AAPL"}}}
	u, err := NewUseCaseWithRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	repo.readErr = errors.New("database unavailable")
	if _, err := u.Import([]domain.Item{{Symbol: "MSFT"}}, ImportReplace, ""); !errors.Is(err, repo.readErr) {
		t.Fatalf("expected read failure, got %v", err)
	}
	if len(repo.items) != 1 || repo.items[0].Symbol != "AAPL" {
		t.Fatalf("failed import changed persisted state: %+v", repo.items)
	}
}
