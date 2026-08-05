package watchlist

import (
	"errors"

	"atlas/internal/domain/watchlist"
)

// UseCase keeps a simple in-memory watchlist of symbols the user wants to track.
type UseCase struct {
	Items []watchlist.Item
}

func NewUseCase() *UseCase {
	return &UseCase{}
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
	u.Items = append(u.Items, watchlist.Item{Symbol: symbol, AddedAt: addedAt})
	return nil
}

// List returns every tracked symbol.
func (u *UseCase) List() ([]watchlist.Item, error) {
	return u.Items, nil
}
