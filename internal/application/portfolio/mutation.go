package portfolio

import (
	domain "atlas/internal/domain/portfolio"
	"errors"
)

// AtomicRepository locks and loads the current database state, applies a pure mutation,
// then commits history and positions together. The callback must not perform network I/O.
type AtomicRepository interface {
	MutatePortfolio(string, func([]domain.Position, []domain.Transaction) ([]domain.Position, []domain.Transaction, error)) error
}

// mutate publishes a working copy only after a successful commit. Caller holds u.mu.
func (u *UseCase) mutate(change func(*UseCase) error) error {
	var next *UseCase
	apply := func(positions []domain.Position, transactions []domain.Transaction) ([]domain.Position, []domain.Transaction, error) {
		next = &UseCase{Positions: cloneWith(positions), Transactions: cloneWith(transactions)}
		if err := change(next); err != nil {
			return nil, nil, err
		}
		return next.Positions, next.Transactions, nil
	}
	if u.Repository != nil {
		repo, ok := u.Repository.(AtomicRepository)
		if !ok {
			return errors.New("repository does not support atomic portfolio mutations")
		}
		if err := repo.MutatePortfolio(u.ActivePortfolioID, apply); err != nil {
			return err
		}
	} else {
		if _, _, err := apply(u.Positions, u.Transactions); err != nil {
			return err
		}
	}
	u.Positions, u.Transactions = next.Positions, next.Transactions
	return nil
}

// ForPortfolio creates a request-owned view; selecting it never changes another request.
//
// repository overrides the shared one for this view — pass the request-scoped handle
// (Storage.WithContext) so the view's queries are cancelled with the request. Passing nil keeps
// the shared repository, which is what background callers want.
//
// This parameter exists because scoping only Application.Storage was not enough: the view built
// here carried the *original* repository, so an HTTP portfolio write ignored the request context
// entirely, however carefully the handler had scoped its storage handle.
func (u *UseCase) ForPortfolio(id string, repository Repository) (*UseCase, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Repository == nil {
		return u, nil
	}
	if repository == nil {
		repository = u.Repository
	}
	scoped := &UseCase{Repository: repository, ExchangeRates: u.ExchangeRates, ActivePortfolioID: id}
	if err := scoped.reload(); err != nil {
		return nil, err
	}
	return scoped, nil
}
