package portfolio

import "atlas/internal/domain/portfolio"

type Engine interface {
	Positions() ([]portfolio.Position, error)
	Transactions() ([]portfolio.Transaction, error)
	ROI() (float64, error)
	Risk() (portfolio.RiskSnapshot, error)
}
