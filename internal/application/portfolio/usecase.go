package portfolio

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"atlas/internal/domain/currency"
	"atlas/internal/domain/portfolio"
)

// ExchangeRateSource converts an amount in a non-base currency to currency.Base (PLN) — see
// exchangerate.UseCase.ToBase, which satisfies this. Kept as a small interface here (rather than
// importing the concrete use case) so portfolio stays testable without a live NBP/Redis stack,
// matching how Repository is already an interface, not a concrete persistence type.
type ExchangeRateSource interface {
	ToBase(amount float64, code currency.Code) (float64, error)
}

// UseCase keeps portfolio logic deterministic and testable. Positions/Transactions are an
// in-memory working copy; when Repository is set, it's hydrated from persisted state on
// construction and written through on every mutation, so state survives restarts.
type UseCase struct {
	Positions    []portfolio.Position
	Transactions []portfolio.Transaction
	Repository   Repository

	// ExchangeRates converts a non-PLN transaction's Price into PriceInPLN at record time (see
	// RecordTransaction). A PLN transaction (the default when Currency is left empty) never
	// needs it — that path works with ExchangeRates nil, so existing tests/callers can construct
	// a UseCase without a live NBP/Redis stack. Recording a non-PLN transaction with
	// ExchangeRates nil is an error, deliberately not a silent 1:1 fallback: treating a $100
	// price as if it were 100 PLN would misrepresent real money, unlike the "feature simply
	// isn't active" non-fatal-optional pattern ML/Discord use elsewhere in this codebase.
	ExchangeRates ExchangeRateSource
}

func NewUseCase() *UseCase {
	return &UseCase{}
}

// NewUseCaseWithRepository hydrates in-memory state from the repository up front. A load
// failure here is returned rather than silently starting empty, matching the rest of the
// app's "no silent degraded mode" stance.
func NewUseCaseWithRepository(repo Repository) (*UseCase, error) {
	uc := &UseCase{Repository: repo}
	if err := uc.Reload(); err != nil {
		return nil, err
	}
	return uc, nil
}

// Reload re-reads Positions/Transactions from Repository, discarding whatever the in-memory
// working copy currently holds — used after an out-of-band bulk change to the underlying data
// (persistence.TimescaleDB's FlushAll/RestoreAll, wired up from the Account page's Backup/
// Restore/Flush buttons) that per-mutation methods like RecordTransaction can't express. A nil
// Repository just clears the in-memory copy, matching NewUseCase's zero-value state.
func (u *UseCase) Reload() error {
	if u.Repository == nil {
		u.Positions, u.Transactions = nil, nil
		return nil
	}
	positions, err := u.Repository.GetPositions()
	if err != nil {
		return err
	}
	transactions, err := u.Repository.GetTransactions()
	if err != nil {
		return err
	}
	u.Positions, u.Transactions = positions, transactions
	return nil
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
	// A caller that never sets Timestamp (both the dashboard form and the JSON API leave it out)
	// would otherwise store "" here. persistence.SaveTransaction already falls back to time.Now
	// for what it writes to the DB, but that corrected value never makes it back into tx — so
	// the in-memory Transactions slice (what GetTransactions/the dashboard actually reads until
	// the next restart) kept the empty string, rendering as the zero time "0001-01-01 00:00".
	if tx.Timestamp == "" {
		tx.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	if tx.Currency == "" {
		tx.Currency = string(currency.Base)
	}
	if !currency.Valid(currency.Code(tx.Currency)) {
		return fmt.Errorf("unsupported currency %q", tx.Currency)
	}
	switch {
	case currency.Code(tx.Currency) == currency.Base:
		tx.PriceInPLN = tx.Price
	case u.ExchangeRates == nil:
		return fmt.Errorf("cannot record a %s transaction: no exchange rate source configured", tx.Currency)
	default:
		priceInPLN, err := u.ExchangeRates.ToBase(tx.Price, currency.Code(tx.Currency))
		if err != nil {
			return fmt.Errorf("convert %s price to %s: %w", tx.Currency, currency.Base, err)
		}
		tx.PriceInPLN = priceInPLN
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
	return u.applyPositionUpdate(position)
}

// applyPositionUpdate folds a freshly computed position into u.Positions and the repository: a
// position that still holds a nonzero quantity is upserted, one that's back to zero (a symbol
// fully sold) is dropped entirely — a closed position lives on only in transaction history, not
// as a lingering zero-quantity row someone has to notice and delete by hand. Shared by
// RecordTransaction (append path) and recomputePosition (replay-after-delete path) so the two
// can't drift on this rule.
func (u *UseCase) applyPositionUpdate(position portfolio.Position) error {
	if position.Quantity == 0 {
		return u.RemovePosition(position.Symbol)
	}
	if index := findPositionIndex(u.Positions, position.Symbol); index >= 0 {
		u.Positions[index] = position
	} else {
		u.Positions = append(u.Positions, position)
	}
	if u.Repository != nil {
		return u.Repository.SavePositions(u.Positions)
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

// RemovePositions removes several positions at once (the dashboard's checkbox-select bulk
// delete) — each symbol still goes through RemovePosition individually rather than a single
// batched repository call, same reasoning as RemoveTransactions: it's simple and already
// correct one at a time. Errors from individual symbols are joined rather than aborting the
// whole batch.
func (u *UseCase) RemovePositions(symbols []string) error {
	var errs []error
	for _, symbol := range symbols {
		if err := u.RemovePosition(symbol); err != nil {
			errs = append(errs, fmt.Errorf("remove position %s: %w", symbol, err))
		}
	}
	return errors.Join(errs...)
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

	return u.applyPositionUpdate(u.recomputePosition(symbol))
}

// RemoveTransactions deletes several historical transactions by id (bulk delete from the
// dashboard's checkbox selection). Each id goes through RemoveTransaction individually — not a
// single batched repository call — because it's the affected symbols' *position recompute* that
// matters, not the delete itself: recomputePosition already replays whatever remains after each
// removal, so deleting one-at-a-time keeps that invariant trivially correct even when a batch
// spans several symbols. A failure on one id doesn't stop the rest; all errors are joined and
// returned together so the caller can report exactly which ones failed.
func (u *UseCase) RemoveTransactions(ids []string) error {
	var errs []error
	for _, id := range ids {
		if err := u.RemoveTransaction(id); err != nil {
			errs = append(errs, fmt.Errorf("remove transaction %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// recomputePosition rebuilds a symbol's position from scratch by replaying every remaining
// transaction for that symbol in chronological order — pure and side-effect free; callers pass
// the result to applyPositionUpdate to fold it back into u.Positions/the repository. Transactions
// with an unparseable timestamp keep their existing relative order (stable sort) rather than
// being dropped or erroring — the same "don't fail a trade record over a cosmetic field" stance
// RecordTransaction already takes for persistence. A symbol with no transactions left at all
// naturally comes back as the zero-quantity Position{Symbol: symbol}, which applyPositionUpdate
// treats as "remove it" — the same rule a real SELL-to-zero follows.
func (u *UseCase) recomputePosition(symbol string) portfolio.Position {
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
	return position
}

// applyTransaction folds one transaction's effect into a position: weighted-average cost on
// BUY, quantity reduction floored at zero on SELL. Uses PriceInPLN, not Price — position math
// always happens in currency.Base regardless of what currency individual transactions were
// recorded in (see domain/portfolio.Transaction's doc comment), so a symbol bought partly in USD
// and partly in PLN still has one mathematically coherent cost basis. Shared between
// RecordTransaction (append path) and recomputePosition (replay-after-delete path) so the two
// can never drift apart. Callers are responsible for validating tx.Type beforehand
// (RecordTransaction does); any other type is a no-op here.
func applyTransaction(position portfolio.Position, tx portfolio.Transaction) portfolio.Position {
	switch tx.Type {
	case "BUY":
		position.Quantity += tx.Quantity
		position.AverageCost = ((position.AverageCost * (position.Quantity - tx.Quantity)) + (tx.PriceInPLN * tx.Quantity)) / position.Quantity
	case "SELL":
		position.Quantity -= tx.Quantity
		if position.Quantity < 0 {
			position.Quantity = 0
		}
	}
	position.CurrentPrice = tx.PriceInPLN
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

// ComputeRisk returns a lightweight risk snapshot for the live portfolio state (u.Positions —
// see applyTransaction's CurrentPrice, set from each symbol's last recorded trade, not a live
// quote). See ComputeRiskSnapshot for what's actually computed; this method just supplies the
// positions the decision engine and JSON API already expect to read.
func (u *UseCase) ComputeRisk() (portfolio.RiskSnapshot, error) {
	return ComputeRiskSnapshot(u.Positions), nil
}

// ComputeRiskSnapshot computes a lightweight risk snapshot from positions based on exposure,
// drawdown, and volatility proxies. A pure function of its argument (not a UseCase method) so a
// caller can feed it a different price basis than u.Positions' own — see
// internal/app/web_handlers.go's livePricedPositions, which overlays each position's live quote
// before calling this, so the Dashboard/Portfolio pages show risk against the current market
// instead of each symbol's last trade price.
//
// Drawdown here is a current-state proxy — total unrealized loss across losing positions, as a
// fraction of total exposure (negative; 0 when nothing is underwater) — not a true historical
// peak-to-trough max drawdown. A real max drawdown needs a portfolio equity curve over time,
// which needs portfolio snapshot persistence (not implemented — see PROJECT_CONTEXT.md); this
// is what's computable from current position state alone.
func ComputeRiskSnapshot(positions []portfolio.Position) portfolio.RiskSnapshot {
	if len(positions) == 0 {
		return portfolio.RiskSnapshot{Exposure: 0, Drawdown: 0, Volatility: 0}
	}

	exposure := 0.0
	unrealizedLoss := 0.0
	volatility := 0.0
	for _, position := range positions {
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
		Volatility: volatility / float64(len(positions)),
	}
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
