package portfolio

import "atlas/internal/domain/portfolio"

// Repository persists portfolio state so positions and transaction history survive restarts.
type Repository interface {
	GetPositions() ([]portfolio.Position, error)
	SavePositions(positions []portfolio.Position) error
	SaveTransaction(tx portfolio.Transaction) error
	DeletePosition(symbol string) error
}
