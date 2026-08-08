package watchlist

import (
	"errors"
	"fmt"

	"atlas/internal/domain/watchlist"
)

// UseCase keeps a watchlist of symbols the user wants to track. Items is an in-memory
// working copy; when Repository is set, it's hydrated from persisted state on construction
// and written through on every mutation, so tracked symbols survive restarts.
type UseCase struct {
	Items      []watchlist.Item
	Repository Repository
}

func NewUseCase() *UseCase {
	return &UseCase{}
}

// NewUseCaseWithRepository hydrates in-memory state from the repository up front. A load
// failure here is returned rather than silently starting empty.
func NewUseCaseWithRepository(repo Repository) (*UseCase, error) {
	uc := &UseCase{Repository: repo}
	if repo == nil {
		return uc, nil
	}
	items, err := repo.GetItems()
	if err != nil {
		return nil, err
	}
	uc.Items = items
	return uc, nil
}

// Add tracks a symbol. Adding an already-tracked symbol is a no-op.
func (u *UseCase) Add(symbol string, addedAt string) error {
	if symbol == "" {
		return errors.New("symbol is required")
	}
	for _, item := range u.Items {
		if item.Symbol == symbol {
			return nil
		}
	}
	item := watchlist.Item{Symbol: symbol, AddedAt: addedAt}
	u.Items = append(u.Items, item)
	if u.Repository != nil {
		if err := u.Repository.SaveItem(item); err != nil {
			return err
		}
	}
	return nil
}

// List returns every tracked symbol.
func (u *UseCase) List() ([]watchlist.Item, error) {
	return u.Items, nil
}

// Remove untracks a symbol. Removing a symbol that isn't tracked is a no-op, matching Add's
// idempotent stance.
func (u *UseCase) Remove(symbol string) error {
	if symbol == "" {
		return errors.New("symbol is required")
	}
	kept := u.Items[:0:0]
	for _, item := range u.Items {
		if item.Symbol != symbol {
			kept = append(kept, item)
		}
	}
	u.Items = kept
	if u.Repository != nil {
		return u.Repository.DeleteItem(symbol)
	}
	return nil
}

// RemoveMany untracks several symbols at once (the dashboard's checkbox-select bulk delete) —
// each symbol still goes through Remove individually rather than a single batched repository
// call, matching portfolio.UseCase.RemovePositions/RemoveTransactions' reasoning. Errors from
// individual symbols are joined rather than aborting the whole batch.
func (u *UseCase) RemoveMany(symbols []string) error {
	var errs []error
	for _, symbol := range symbols {
		if err := u.Remove(symbol); err != nil {
			errs = append(errs, fmt.Errorf("remove watchlist item %s: %w", symbol, err))
		}
	}
	return errors.Join(errs...)
}
