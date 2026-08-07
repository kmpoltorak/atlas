package portfolio

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"atlas/internal/domain/portfolio"
)

// UseCase keeps portfolio logic deterministic and testable. Positions/Transactions are an
// in-memory working copy; when Repository is set, it's hydrated from persisted state on
// construction and written through on every mutation, so state survives restarts.
type UseCase struct {
	Positions    []portfolio.Position
	Transactions []portfolio.Transaction
	Repository   Repository
}

func NewUseCase() *UseCase {
	return &UseCase{}
}

// NewUseCaseWithRepository hydrates in-memory state from the repository up front. A load
// failure here is returned rather than silently starting empty, matching the rest of the
// app's "no silent degraded mode" stance.
func NewUseCaseWithRepository(repo Repository) (*UseCase, error) {
	uc := &UseCase{Repository: repo}
	if repo == nil {
		return uc, nil
	}
	positions, err := repo.GetPositions()
	if err != nil {
		return nil, err
	}
	uc.Positions = positions
	transactions, err := repo.GetTransactions()
	if err != nil {
		return nil, err
	}
	uc.Transactions = transactions
	return uc, nil
}

// GetPositions returns the current snapshot held by the use case.
func (u *UseCase) GetPositions() ([]portfolio.Position, error) {
	return u.Positions, nil
}

// GetTransactions returns every recorded transaction held by the use case, most recent first
// when Repository-backed (see TimescaleDB.GetTransactions); insertion order otherwise.
func (u *UseCase) GetTransactions() ([]portfolio.Transaction, error) {
	return u.Transactions, nil
}

// RecordTransaction appends a transaction and updates the in-memory position state.
func (u *UseCase) RecordTransaction(tx portfolio.Transaction) error {
	if tx.Symbol == "" {
		return errors.New("symbol is required")
	}
	if tx.Price <= 0 {
		return errors.New("transaction price must be positive")
	}
	if tx.Quantity == 0 {
		return errors.New("transaction quantity must not be zero")
	}
	if tx.Type != "BUY" && tx.Type != "SELL" {
		return errors.New("transaction type is unsupported")
	}

	if u.Repository != nil {
		id, err := u.Repository.SaveTransaction(tx)
		if err != nil {
			return err
		}
		tx.ID = id
	} else {
		id, err := randomTransactionID()
		if err != nil {
			return err
		}
		tx.ID = id
	}
	u.Transactions = append(u.Transactions, tx)

	position := applyTransaction(findPosition(u.Positions, tx.Symbol), tx)
	if index := findPositionIndex(u.Positions, tx.Symbol); index >= 0 {
		u.Positions[index] = position
	} else {
		u.Positions = append(u.Positions, position)
	}

	if u.Repository != nil {
		if err := u.Repository.SavePositions(u.Positions); err != nil {
			return err
		}
	}
	return nil
}

// RemovePosition deletes a symbol's current position entirely (e.g. a test entry recorded by
// mistake) — for correcting/removing state, not for recording a real SELL, which is what
// RecordTransaction is for. Removing a symbol with no position is a no-op. The transaction
// history for that symbol is deliberately left untouched (an append-only audit log, matching
// recommendations/predictions elsewhere in this codebase), so this only clears the current
// aggregate, not the record of what happened.
func (u *UseCase) RemovePosition(symbol string) error {
	if symbol == "" {
		return errors.New("symbol is required")
	}
	kept := u.Positions[:0:0]
	for _, position := range u.Positions {
		if position.Symbol != symbol {
			kept = append(kept, position)
		}
	}
	u.Positions = kept
	if u.Repository != nil {
		return u.Repository.DeletePosition(symbol)
	}
	return nil
}

// RemoveTransaction deletes one historical transaction by id (e.g. a mis-entered trade) and
// recomputes the affected symbol's position from scratch by replaying its remaining
// transactions in chronological order — the only reliable way to undo a transaction's effect on
// weighted-average cost once later transactions have layered on top of it; an in-place "subtract
// this one back out" can't be reversed cleanly in general. Removing an unknown id is a no-op,
// matching RemovePosition's convention.
func (u *UseCase) RemoveTransaction(id string) error {
	if id == "" {
		return errors.New("id is required")
	}
	index := -1
	for i, t := range u.Transactions {
		if t.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return nil
	}
	symbol := u.Transactions[index].Symbol
	u.Transactions = append(u.Transactions[:index:index], u.Transactions[index+1:]...)

	if u.Repository != nil {
		if err := u.Repository.DeleteTransaction(id); err != nil {
			return err
		}
	}

	u.recomputePosition(symbol)

	if u.Repository != nil {
		return u.Repository.SavePositions(u.Positions)
	}
	return nil
}

// recomputePosition rebuilds a symbol's position from scratch by replaying every remaining
// transaction for that symbol in chronological order. Transactions with an unparseable
// timestamp keep their existing relative order (stable sort) rather than being dropped or
// erroring — the same "don't fail a trade record over a cosmetic field" stance RecordTransaction
// already takes for persistence.
func (u *UseCase) recomputePosition(symbol string) {
	var symbolTx []portfolio.Transaction
	for _, t := range u.Transactions {
		if t.Symbol == symbol {
			symbolTx = append(symbolTx, t)
		}
	}
	sort.SliceStable(symbolTx, func(i, j int) bool {
		ti, iErr := time.Parse(time.RFC3339, symbolTx[i].Timestamp)
		tj, jErr := time.Parse(time.RFC3339, symbolTx[j].Timestamp)
		if iErr != nil || jErr != nil {
			return false
		}
		return ti.Before(tj)
	})

	position := portfolio.Position{Symbol: symbol}
	for _, t := range symbolTx {
		position = applyTransaction(position, t)
	}

	if index := findPositionIndex(u.Positions, symbol); index >= 0 {
		u.Positions[index] = position
	} else if len(symbolTx) > 0 {
		u.Positions = append(u.Positions, position)
	}
}

// applyTransaction folds one transaction's effect into a position: weighted-average cost on
// BUY, quantity reduction floored at zero on SELL. Shared between RecordTransaction (append
// path) and recomputePosition (replay-after-delete path) so the two can never drift apart.
// Callers are responsible for validating tx.Type beforehand (RecordTransaction does); any other
// type is a no-op here.
func applyTransaction(position portfolio.Position, tx portfolio.Transaction) portfolio.Position {
	switch tx.Type {
	case "BUY":
		position.Quantity += tx.Quantity
		position.AverageCost = ((position.AverageCost * (position.Quantity - tx.Quantity)) + (tx.Price * tx.Quantity)) / position.Quantity
	case "SELL":
		position.Quantity -= tx.Quantity
		if position.Quantity < 0 {
			position.Quantity = 0
		}
	}
	position.CurrentPrice = tx.Price
	return position
}

func randomTransactionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate transaction id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// ComputeROI calculates the weighted ROI across positions.
func (u *UseCase) ComputeROI() (float64, error) {
	if len(u.Positions) == 0 {
		return 0, nil
	}
	totalWeight := 0.0
	totalQuantity := 0.0
	for _, position := range u.Positions {
		if position.AverageCost <= 0 || position.Quantity <= 0 {
			continue
		}
		roi := ((position.CurrentPrice - position.AverageCost) / position.AverageCost) * 100.0
		totalWeight += roi * position.Quantity
		totalQuantity += position.Quantity
	}
	if totalQuantity == 0 {
		return 0, nil
	}
	return totalWeight / totalQuantity, nil
}

// ComputeRisk returns a lightweight risk snapshot based on exposure, drawdown, and volatility
// proxies.
//
// Drawdown here is a current-state proxy — total unrealized loss across losing positions, as a
// fraction of total exposure (negative; 0 when nothing is underwater) — not a true historical
// peak-to-trough max drawdown. A real max drawdown needs a portfolio equity curve over time,
// which needs portfolio snapshot persistence (not implemented — see PROJECT_CONTEXT.md); this
// is what's computable from current position state alone.
func (u *UseCase) ComputeRisk() (portfolio.RiskSnapshot, error) {
	if len(u.Positions) == 0 {
		return portfolio.RiskSnapshot{Exposure: 0, Drawdown: 0, Volatility: 0}, nil
	}

	exposure := 0.0
	unrealizedLoss := 0.0
	volatility := 0.0
	for _, position := range u.Positions {
		exposure += position.CurrentPrice * position.Quantity
		volatility += math.Abs(position.CurrentPrice - position.AverageCost)
		if pnl := (position.CurrentPrice - position.AverageCost) * position.Quantity; pnl < 0 {
			unrealizedLoss += pnl
		}
	}

	drawdown := 0.0
	if exposure > 0 {
		drawdown = unrealizedLoss / exposure
	}

	return portfolio.RiskSnapshot{
		Exposure:   exposure,
		Drawdown:   drawdown,
		Volatility: volatility / float64(len(u.Positions)),
	}, nil
}

func findPosition(positions []portfolio.Position, symbol string) portfolio.Position {
	for _, position := range positions {
		if position.Symbol == symbol {
			return position
		}
	}
	return portfolio.Position{Symbol: symbol, Quantity: 0, AverageCost: 0, CurrentPrice: 0}
}

func findPositionIndex(positions []portfolio.Position, symbol string) int {
	for index, position := range positions {
		if position.Symbol == symbol {
			return index
		}
	}
	return -1
}
