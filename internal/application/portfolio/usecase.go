package portfolio

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"atlas/internal/domain/currency"
	"atlas/internal/domain/portfolio"
)

// MaxPortfolios caps how many portfolios one Atlas install may have at once (see
// UseCase.CreatePortfolio) — enough to separate "main/safe/speculative"-style buckets without
// the dashboard's portfolio tabs needing to scroll.
const MaxPortfolios = 5

// ExchangeRateSource converts an amount in a non-base currency to currency.Base (PLN) — see
// exchangerate.UseCase.ToBase, which satisfies this. Kept as a small interface here (rather than
// importing the concrete use case) so portfolio stays testable without a live NBP/Redis stack,
// matching how Repository is already an interface, not a concrete persistence type.
type ExchangeRateSource interface {
	ToBase(amount float64, code currency.Code) (float64, error)
}

// UseCase keeps portfolio logic deterministic and testable. Positions/Transactions are an
// in-memory working copy of ActivePortfolioID's data; when Repository is set, it's hydrated from
// persisted state on construction and written through on every mutation, so state survives
// restarts.
//
// ActivePortfolioID/Positions/Transactions/Portfolios are shared mutable state on one UseCase
// instance (internal/app.Application wires up exactly one, used by every concurrent request), so
// every method that reads or writes them takes mu — a plain sync.Mutex, not RWMutex: even a
// "read" (GetPositions, Snapshot, …) needs the lock to hand back a defensive copy rather than a
// slice aliasing the live backing array, since SwitchPortfolio/RecordTransaction/etc. mutate
// elements of that array in place (see applyPositionUpdate). Two browser tabs switching
// portfolios at the same moment now serialize on mu instead of racing; Snapshot exists so a page
// render reads Positions/Transactions/ActivePortfolioID/Portfolios as of one consistent instant,
// not four separate locked calls a concurrent SwitchPortfolio could interleave between.
//
// Exported fields are still read directly by tests that build a UseCase by hand before any
// concurrent access starts (e.g. `uc := &UseCase{Positions: ...}`) — that's fine, mu only matters
// once more than one goroutine can reach the same instance, which is exactly the production
// wiring this exists for.
type UseCase struct {
	mu sync.Mutex

	Positions    []portfolio.Position
	Transactions []portfolio.Transaction
	Repository   Repository

	// ActivePortfolioID selects which portfolio Positions/Transactions/RecordTransaction/etc.
	// read and write — see SwitchPortfolio. Portfolios caches Repository.ListPortfolios() so
	// callers (e.g. the dashboard's portfolio tabs) don't need their own round-trip just to
	// render the tab list.
	ActivePortfolioID string
	Portfolios        []portfolio.Portfolio

	// ExchangeRates converts a non-PLN transaction's Price into PriceInPLN at record time (see
	// RecordTransaction). A PLN transaction (the default when Currency is left empty) never
	// needs it — that path works with ExchangeRates nil, so existing tests/callers can construct
	// a UseCase without a live NBP/Redis stack. Recording a non-PLN transaction with
	// ExchangeRates nil is an error, deliberately not a silent 1:1 fallback: treating a $100
	// price as if it were 100 PLN would misrepresent real money, unlike the "feature simply
	// isn't active" non-fatal-optional pattern ML/Discord use elsewhere in this codebase.
	// Set once at construction (internal/app/app.go), never reassigned afterward, so reading it
	// doesn't need mu.
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

// Reload re-reads Portfolios and ActivePortfolioID's Positions/Transactions from Repository,
// discarding whatever the in-memory working copy currently holds — used after an out-of-band bulk
// change to the underlying data (persistence.TimescaleDB's FlushAll/RestoreAll, wired up from the
// Account page's Backup/Restore/Flush buttons) that per-mutation methods like RecordTransaction
// can't express, as well as by SwitchPortfolio and NewUseCaseWithRepository. A nil Repository just
// clears the in-memory copy, matching NewUseCase's zero-value state.
//
// If ActivePortfolioID is empty or no longer among Portfolios (e.g. Flush wiped every portfolio,
// or SwitchPortfolio was given an unknown id), it falls back to the first portfolio —
// Repository.ListPortfolios guarantees at least one exists, bootstrapping it if needed.
func (u *UseCase) Reload() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.reload()
}

// reload is Reload's lock-free core — called both directly (via Reload) and by SwitchPortfolio/
// DeletePortfolio, which need it folded into their own already-held lock rather than taking mu a
// second time.
func (u *UseCase) reload() error {
	if u.Repository == nil {
		u.Positions, u.Transactions = nil, nil
		return nil
	}
	portfolios, err := u.Repository.ListPortfolios()
	if err != nil {
		return err
	}
	u.Portfolios = portfolios
	if !portfolioIDExists(portfolios, u.ActivePortfolioID) {
		u.ActivePortfolioID = portfolios[0].ID
	}

	positions, err := u.Repository.GetPositions(u.ActivePortfolioID)
	if err != nil {
		return err
	}
	transactions, err := u.Repository.GetTransactions(u.ActivePortfolioID)
	if err != nil {
		return err
	}
	u.Positions, u.Transactions = positions, transactions
	return nil
}

func portfolioIDExists(portfolios []portfolio.Portfolio, id string) bool {
	if id == "" {
		return false
	}
	for _, p := range portfolios {
		if p.ID == id {
			return true
		}
	}
	return false
}

// SwitchPortfolio makes id the active portfolio and reloads Positions/Transactions for it — the
// dashboard's portfolio tabs call this before rendering. An unknown id falls back to the first
// portfolio (see Reload) rather than erroring, so a stale cookie never breaks the page.
func (u *UseCase) SwitchPortfolio(id string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.ActivePortfolioID = id
	return u.reload()
}

// CreatePortfolio adds a new, empty portfolio — up to MaxPortfolios total — without switching to
// it. The caller (e.g. portfolioSelectHandler) decides whether to switch afterward.
func (u *UseCase) CreatePortfolio(name string) (portfolio.Portfolio, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return portfolio.Portfolio{}, errors.New("name is required")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Repository == nil {
		return portfolio.Portfolio{}, errors.New("no repository configured")
	}
	if len(u.Portfolios) >= MaxPortfolios {
		return portfolio.Portfolio{}, fmt.Errorf("a maximum of %d portfolios is allowed", MaxPortfolios)
	}
	created, err := u.Repository.CreatePortfolio(name)
	if err != nil {
		return portfolio.Portfolio{}, err
	}
	u.Portfolios = append(u.Portfolios, created)
	return created, nil
}

func (u *UseCase) RenamePortfolio(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name is required")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Repository == nil {
		return errors.New("no repository configured")
	}
	if err := u.Repository.RenamePortfolio(id, name); err != nil {
		return err
	}
	for i := range u.Portfolios {
		if u.Portfolios[i].ID == id {
			u.Portfolios[i].Name = name
		}
	}
	return nil
}

// DeletePortfolio removes a portfolio and everything in it. Refuses to delete the last remaining
// one — Atlas always has somewhere to record a transaction. Deleting the active portfolio falls
// back to whatever Reload picks next (the new first portfolio).
func (u *UseCase) DeletePortfolio(id string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Repository == nil {
		return errors.New("no repository configured")
	}
	if len(u.Portfolios) <= 1 {
		return errors.New("cannot delete the only remaining portfolio")
	}
	if err := u.Repository.DeletePortfolio(id); err != nil {
		return err
	}
	wasActive := id == u.ActivePortfolioID
	if wasActive {
		u.ActivePortfolioID = ""
	}
	return u.reload()
}

// GetAllPositions aggregates positions across every portfolio (see
// Repository.GetAllPositions) — used by the decision engine, not by anything that renders a
// single portfolio's own page. Falls back to the active portfolio's Positions when Repository is
// nil (test-only in-memory mode never has more than one portfolio anyway).
func (u *UseCase) GetAllPositions() ([]portfolio.Position, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Repository != nil {
		return u.Repository.GetAllPositions()
	}
	return cloneWith(u.Positions), nil
}

// GetPositions returns the current snapshot held by the use case — a defensive copy, since
// SwitchPortfolio/RecordTransaction/etc. can mutate the live Positions backing array in place
// from another concurrent request.
func (u *UseCase) GetPositions() ([]portfolio.Position, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return cloneWith(u.Positions), nil
}

// GetTransactions returns every recorded transaction held by the use case, most recent first
// when Repository-backed (see TimescaleDB.GetTransactions); insertion order otherwise. A
// defensive copy, for the same reason as GetPositions.
func (u *UseCase) GetTransactions() ([]portfolio.Transaction, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return cloneWith(u.Transactions), nil
}

// Snapshot is a single consistent read of everything a page render needs — Positions,
// Transactions, ActivePortfolioID, and the Portfolios tab list — taken under one lock so a
// concurrent SwitchPortfolio from another browser tab can never be observed half-applied (e.g.
// the new ActivePortfolioID paired with the previous portfolio's Positions, which four separate
// locked calls could otherwise interleave with).
func (u *UseCase) Snapshot() (positions []portfolio.Position, transactions []portfolio.Transaction, activePortfolioID string, portfolios []portfolio.Portfolio) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return cloneWith(u.Positions), cloneWith(u.Transactions), u.ActivePortfolioID, cloneWith(u.Portfolios)
}

// ActivePortfolio returns which portfolio id Positions/Transactions currently reflect.
func (u *UseCase) ActivePortfolio() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.ActivePortfolioID
}

// PortfolioList returns every portfolio (the dashboard's tab list, and the Account page's
// "manage portfolios" list) — a defensive copy, for the same reason as GetPositions.
func (u *UseCase) PortfolioList() []portfolio.Portfolio {
	u.mu.Lock()
	defer u.mu.Unlock()
	return cloneWith(u.Portfolios)
}

// cloneWith returns a copy of s — GetPositions/GetTransactions/Snapshot/PortfolioList all hand
// out copies rather than the live slice, so a caller iterating the result can't observe
// SwitchPortfolio/RecordTransaction/etc. mutating elements of the backing array in place from
// another concurrent request after the lock protecting this read has already been released.
func cloneWith[T any](s []T) []T {
	if s == nil {
		return nil
	}
	return append(make([]T, 0, len(s)), s...)
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

	// Locked from here on, not from the top of the function: everything above only validates tx
	// and (for a non-PLN transaction) calls out to ExchangeRates.ToBase, an external HTTP round
	// trip that must not hold mu — the point of ExchangeRateSource being pluggable is that
	// currency conversion is a separate, potentially slow concern from the in-memory mutation
	// below, and blocking every other portfolio request on a live NBP call would defeat that.
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.Repository != nil {
		id, err := u.Repository.SaveTransaction(u.ActivePortfolioID, tx)
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
// can't drift on this rule. Lock-free: every caller already holds mu.
func (u *UseCase) applyPositionUpdate(position portfolio.Position) error {
	if position.Quantity == 0 {
		return u.removePosition(position.Symbol)
	}
	if index := findPositionIndex(u.Positions, position.Symbol); index >= 0 {
		u.Positions[index] = position
	} else {
		u.Positions = append(u.Positions, position)
	}
	if u.Repository != nil {
		return u.Repository.SavePositions(u.ActivePortfolioID, u.Positions)
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
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.removePosition(symbol)
}

// removePosition is RemovePosition's lock-free core — also called by applyPositionUpdate
// (RecordTransaction/RemoveTransaction's SELL-to-zero path) and RemovePositions' batch loop,
// both of which already hold mu themselves.
func (u *UseCase) removePosition(symbol string) error {
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
		return u.Repository.DeletePosition(u.ActivePortfolioID, symbol)
	}
	return nil
}

// RemovePositions removes several positions at once (the dashboard's checkbox-select bulk
// delete) — each symbol still goes through the same per-symbol logic as RemovePosition rather
// than a single batched repository call, same reasoning as RemoveTransactions: it's simple and
// already correct one at a time. The whole batch runs under one lock acquisition, not one per
// symbol, so it can't interleave with a concurrent SwitchPortfolio mid-batch. Errors from
// individual symbols are joined rather than aborting the rest.
func (u *UseCase) RemovePositions(symbols []string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	var errs []error
	for _, symbol := range symbols {
		if err := u.removePosition(symbol); err != nil {
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
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.removeTransaction(id)
}

// removeTransaction is RemoveTransaction's lock-free core — also called by RemoveTransactions'
// batch loop, which already holds mu itself.
func (u *UseCase) removeTransaction(id string) error {
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
// dashboard's checkbox selection). Each id goes through the same per-id logic as
// RemoveTransaction — not a single batched repository call — because it's the affected symbols'
// *position recompute* that matters, not the delete itself: recomputePosition already replays
// whatever remains after each removal, so deleting one-at-a-time keeps that invariant trivially
// correct even when a batch spans several symbols. The whole batch runs under one lock
// acquisition, not one per id, so it can't interleave with a concurrent SwitchPortfolio
// mid-batch. A failure on one id doesn't stop the rest; all errors are joined and returned
// together so the caller can report exactly which ones failed.
func (u *UseCase) RemoveTransactions(ids []string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	var errs []error
	for _, id := range ids {
		if err := u.removeTransaction(id); err != nil {
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
// treats as "remove it" — the same rule a real SELL-to-zero follows. Lock-free: its one caller,
// removeTransaction, already holds mu.
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

// ComputeRisk returns a lightweight risk snapshot for the live portfolio state (u.Positions —
// see applyTransaction's CurrentPrice, set from each symbol's last recorded trade, not a live
// quote). See ComputeRiskSnapshot for what's actually computed; this method just supplies the
// positions the decision engine and JSON API already expect to read.
func (u *UseCase) ComputeRisk() (portfolio.RiskSnapshot, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
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
