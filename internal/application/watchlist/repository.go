package watchlist

import "atlas/internal/domain/watchlist"

// Repository persists watchlist state so tracked symbols survive restarts.
type Repository interface {
	GetItems() ([]watchlist.Item, error)
	SaveItem(item watchlist.Item) error
	DeleteItem(symbol string) error
}

// TransactionalRepository is the optional extension a repository implements to make an import
// atomic: read the current items, apply a pure plan, write the result, all in one transaction
// under a lock every other watchlist mutation also takes.
//
// Separate from Repository because the in-memory path has nothing to serialize, and because an
// import is the only operation that needs read-modify-write over the whole list.
type TransactionalRepository interface {
	ReplaceWatchlist(plan func(current []watchlist.Item) ([]watchlist.Item, error)) ([]watchlist.Item, error)
}
