package watchlist

import "atlas/internal/domain/watchlist"

// Repository persists watchlist state so tracked symbols survive restarts.
type Repository interface {
	GetItems() ([]watchlist.Item, error)
	SaveItem(item watchlist.Item) error
	DeleteItem(symbol string) error
}
