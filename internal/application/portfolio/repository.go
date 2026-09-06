package portfolio

import "atlas/internal/domain/portfolio"

// Repository persists portfolio state so positions and transaction history survive restarts.
// Every position/transaction method is scoped to one portfolioID (see UseCase.ActivePortfolioID)
// except GetAllPositions, which deliberately reads across every portfolio at once — the decision
// engine's HasPosition/risk scoring treats "do I hold this symbol" as a whole-account question,
// not a per-portfolio one (see decision.Orchestrator.blendMLAndPortfolio).
// There are deliberately no per-row write methods here. Every mutation goes through
// AtomicRepository.MutatePortfolio — one transaction that commits the ledger and the recomputed
// positions together (see mutation.go). Individual SavePositions/SaveTransaction/Delete* methods
// still exist on the persistence type, but keeping them in this interface meant the use case had
// a second, non-atomic write path it could fall back to by accident: exactly the half-committed
// state MutatePortfolio was introduced to eliminate. A write path that must not be used is better
// removed than commented.
type Repository interface {
	GetPositions(portfolioID string) ([]portfolio.Position, error)
	GetTransactions(portfolioID string) ([]portfolio.Transaction, error)

	// GetAllPositions aggregates every portfolio's positions by symbol (summed quantity,
	// quantity-weighted average cost) — see decision engine usage above.
	GetAllPositions() ([]portfolio.Position, error)

	ListPortfolios() ([]portfolio.Portfolio, error)
	// CreatePortfolio persists a new portfolio and returns it with its generated id. The
	// MaxPortfolios cap is enforced by UseCase, not here, so this stays a plain insert.
	CreatePortfolio(name string) (portfolio.Portfolio, error)
	RenamePortfolio(id, name string) error
	// DeletePortfolio also removes its positions/transactions first (no ON DELETE CASCADE on
	// those FKs — see migrations/001), in one transaction. Refusing to delete the last remaining
	// portfolio is enforced by UseCase, not here.
	DeletePortfolio(id string) error
}
