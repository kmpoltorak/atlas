package portfolio

import "atlas/internal/domain/portfolio"

type Service interface {
	GetPositions() ([]portfolio.Position, error)
	RecordTransaction(tx portfolio.Transaction) error
	ComputeROI() (float64, error)
	ComputeRisk() (portfolio.RiskSnapshot, error)
}
