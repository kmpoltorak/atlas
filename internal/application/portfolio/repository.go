package portfolio

import "atlas/internal/domain/portfolio"

// Repository persists portfolio state so positions and transaction history survive restarts.
type Repository interface {
	GetPositions() ([]portfolio.Position, error)
	SavePositions(positions []portfolio.Position) error
	// SaveTransaction persists a transaction and returns its generated id.
	SaveTransaction(tx portfolio.Transaction) (id string, err error)
	GetTransactions() ([]portfolio.Transaction, error)
	DeleteTransaction(id string) error
	DeletePosition(symbol string) error
}
