package portfolio

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"atlas/internal/domain/currency"
	"atlas/internal/domain/portfolio"
)

// stubExchangeRates is a real, deterministic ExchangeRateSource test double — not a mock of
// business logic, matching this codebase's convention elsewhere (e.g. auth's in-memory session
// store).
type stubExchangeRates struct {
	rates map[currency.Code]float64
	err   error
}

func (s stubExchangeRates) ToBase(amount float64, code currency.Code) (float64, error) {
	if s.err != nil {
		return 0, s.err
	}
	rate, ok := s.rates[code]
	if !ok {
		return 0, errors.New("no rate for currency")
	}
	return amount * rate, nil
}

func TestRecordTransactionUpdatesPositionsAndROI(t *testing.T) {
	uc := NewUseCase()

	buy := portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2024-01-01"}
	if err := uc.RecordTransaction(buy); err != nil {
		t.Fatalf("expected buy transaction to be accepted: %v", err)
	}

	buy2 := portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 5, Price: 110, Timestamp: "2024-01-02"}
	if err := uc.RecordTransaction(buy2); err != nil {
		t.Fatalf("expected second buy transaction to be accepted: %v", err)
	}

	positions, err := uc.GetPositions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected one position, got %d", len(positions))
	}
	if positions[0].Quantity != 15 {
		t.Fatalf("expected total quantity 15, got %v", positions[0].Quantity)
	}

	roi, err := uc.ComputeROI()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if roi != 6.451612903225811 {
		t.Fatalf("expected weighted ROI 6.451612903225811, got %v", roi)
	}
}

// TestRecordTransactionRemovesPositionWhenSoldToZero covers a real bug report: selling an entire
// holding left the position row visible with Quantity 0 instead of disappearing, forcing a
// separate manual "Remove" click even though nothing is actually held anymore — the transaction
// history (the audit trail) is what should stay, not a zeroed-out position row.
func TestRecordTransactionRemovesPositionWhenSoldToZero(t *testing.T) {
	uc := NewUseCase()
	if err := uc.RecordTransaction(portfolio.Transaction{Symbol: "SPCX", Type: "BUY", Quantity: 10, Price: 130.20, Timestamp: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := uc.RecordTransaction(portfolio.Transaction{Symbol: "SPCX", Type: "SELL", Quantity: 10, Price: 131.28, Timestamp: "2026-01-02T00:00:00Z"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	positions, _ := uc.GetPositions()
	if len(positions) != 0 {
		t.Fatalf("expected the fully-sold position to be removed, got %+v", positions)
	}
	transactions, _ := uc.GetTransactions()
	if len(transactions) != 2 {
		t.Fatalf("expected both transactions to remain in history, got %+v", transactions)
	}
}

func TestRecordTransactionRejectsInvalidInput(t *testing.T) {
	uc := NewUseCase()
	if err := uc.RecordTransaction(portfolio.Transaction{Symbol: "", Type: "BUY", Quantity: 1, Price: 1}); err == nil {
		t.Fatal("expected invalid symbol to be rejected")
	}
	if err := uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 0, Price: 1}); err == nil {
		t.Fatal("expected zero quantity to be rejected")
	}
}

func TestRecordTransactionDefaultsToPLNAndNeedsNoExchangeRates(t *testing.T) {
	uc := NewUseCase() // no ExchangeRates set
	tx := portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"}
	if err := uc.RecordTransaction(tx); err != nil {
		t.Fatalf("expected a currency-less (PLN) transaction to need no exchange rate source: %v", err)
	}
	if uc.Transactions[0].Currency != "PLN" {
		t.Fatalf("expected Currency to default to PLN, got %q", uc.Transactions[0].Currency)
	}
	if uc.Transactions[0].PriceInPLN != 100 {
		t.Fatalf("expected PriceInPLN to equal Price for PLN, got %v", uc.Transactions[0].PriceInPLN)
	}
}

func TestRecordTransactionDefaultsTimestampWhenEmpty(t *testing.T) {
	uc := NewUseCase()
	tx := portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100}
	if err := uc.RecordTransaction(tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := uc.Transactions[0].Timestamp; got == "" {
		t.Fatal("expected a non-empty default Timestamp")
	} else if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Fatalf("expected default Timestamp to be RFC3339, got %q: %v", got, err)
	}
}

func TestRecordTransactionRejectsUnsupportedCurrency(t *testing.T) {
	uc := NewUseCase()
	err := uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100, Currency: "GBP"})
	if err == nil {
		t.Fatal("expected an unsupported currency to be rejected")
	}
}

func TestRecordTransactionRejectsNonPLNWithoutExchangeRates(t *testing.T) {
	uc := NewUseCase() // no ExchangeRates set
	err := uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100, Currency: "USD"})
	if err == nil {
		t.Fatal("expected a USD transaction with no configured exchange rate source to be rejected, not silently treated as 1:1 PLN")
	}
}

func TestRecordTransactionConvertsNonPLNPriceUsingExchangeRates(t *testing.T) {
	uc := NewUseCase()
	uc.ExchangeRates = stubExchangeRates{rates: map[currency.Code]float64{currency.USD: 3.73}}

	tx := portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Currency: "USD", Timestamp: "2026-01-01T00:00:00Z"}
	if err := uc.RecordTransaction(tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if uc.Transactions[0].PriceInPLN != 373 {
		t.Fatalf("expected PriceInPLN = 100 * 3.73 = 373, got %v", uc.Transactions[0].PriceInPLN)
	}
	positions, _ := uc.GetPositions()
	if positions[0].AverageCost != 373 || positions[0].CurrentPrice != 373 {
		t.Fatalf("expected the position's AverageCost/CurrentPrice to be in PLN (373), got %+v", positions[0])
	}
}

func TestRecordTransactionPropagatesExchangeRateErrors(t *testing.T) {
	uc := NewUseCase()
	uc.ExchangeRates = stubExchangeRates{err: errors.New("NBP unreachable")}

	err := uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100, Currency: "USD"})
	if err == nil {
		t.Fatal("expected the exchange rate source's error to propagate rather than silently recording a wrong price")
	}
}

// TestMixedCurrencyPositionHasOneCoherentPLNCostBasis proves the scenario the Author explicitly
// wanted: buying the same symbol partly in USD and partly in PLN still produces one correct
// weighted-average cost, computed entirely in PLN.
func TestMixedCurrencyPositionHasOneCoherentPLNCostBasis(t *testing.T) {
	uc := NewUseCase()
	uc.ExchangeRates = stubExchangeRates{rates: map[currency.Code]float64{currency.USD: 4.0}}

	// 10 shares @ $100 (USD) -> 400 PLN/share.
	if err := uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Currency: "USD", Timestamp: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 10 more shares @ 500 PLN/share directly.
	if err := uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 500, Currency: "PLN", Timestamp: "2026-01-02T00:00:00Z"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	positions, _ := uc.GetPositions()
	wantAvgCost := (400.0*10 + 500.0*10) / 20 // 450
	if positions[0].Quantity != 20 {
		t.Fatalf("expected quantity 20, got %v", positions[0].Quantity)
	}
	if positions[0].AverageCost != wantAvgCost {
		t.Fatalf("expected average cost %v (blended in PLN across both currencies), got %v", wantAvgCost, positions[0].AverageCost)
	}
}

func TestComputeRiskWithNoPositionsIsZero(t *testing.T) {
	uc := NewUseCase()
	risk, err := uc.ComputeRisk()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if risk.Exposure != 0 || risk.Drawdown != 0 || risk.Volatility != 0 {
		t.Fatalf("expected a zero-value risk snapshot with no positions, got %+v", risk)
	}
}

func TestComputeRiskDrawdownReflectsOnlyLosingPositions(t *testing.T) {
	uc := NewUseCase()
	uc.Positions = []portfolio.Position{
		{Symbol: "WINNER", Quantity: 10, AverageCost: 100, CurrentPrice: 120}, // +200 unrealized
		{Symbol: "LOSER", Quantity: 10, AverageCost: 100, CurrentPrice: 80},   // -200 unrealized
	}

	risk, err := uc.ComputeRisk()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantExposure := 120*10 + 80*10.0 // 2000
	if risk.Exposure != wantExposure {
		t.Fatalf("expected exposure %v, got %v", wantExposure, risk.Exposure)
	}
	// Only LOSER's -200 counts toward drawdown, not WINNER's +200 — drawdown is downside-only.
	wantDrawdown := -200.0 / wantExposure
	if risk.Drawdown != wantDrawdown {
		t.Fatalf("expected drawdown %v, got %v", wantDrawdown, risk.Drawdown)
	}
}

func TestComputeRiskDrawdownIsZeroWithNoLosingPositions(t *testing.T) {
	uc := NewUseCase()
	uc.Positions = []portfolio.Position{
		{Symbol: "WINNER", Quantity: 10, AverageCost: 100, CurrentPrice: 120},
	}
	risk, err := uc.ComputeRisk()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if risk.Drawdown != 0 {
		t.Fatalf("expected zero drawdown with only winning positions, got %v", risk.Drawdown)
	}
}

func TestRemovePositionDeletesIt(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2024-01-01"})
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "MSFT", Type: "BUY", Quantity: 5, Price: 300, Timestamp: "2024-01-01"})

	if err := uc.RemovePosition("AAPL"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	positions, _ := uc.GetPositions()
	if len(positions) != 1 || positions[0].Symbol != "MSFT" {
		t.Fatalf("expected only MSFT to remain, got %+v", positions)
	}
	// The transaction history is an append-only audit log — removing the position must not
	// erase what actually happened.
	if len(uc.Transactions) != 2 {
		t.Fatalf("expected transaction history to be untouched, got %d transactions", len(uc.Transactions))
	}
}

func TestRemovePositionForUnknownSymbolIsANoop(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2024-01-01"})

	if err := uc.RemovePosition("MSFT"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	positions, _ := uc.GetPositions()
	if len(positions) != 1 || positions[0].Symbol != "AAPL" {
		t.Fatalf("expected AAPL to remain untouched, got %+v", positions)
	}
}

func TestRemovePositionRejectsEmptySymbol(t *testing.T) {
	uc := NewUseCase()
	if err := uc.RemovePosition(""); err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

func TestRemovePositionsDeletesEachSelectedSymbol(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2024-01-01"})
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "MSFT", Type: "BUY", Quantity: 5, Price: 300, Timestamp: "2024-01-01"})
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "GOOG", Type: "BUY", Quantity: 2, Price: 150, Timestamp: "2024-01-01"})

	if err := uc.RemovePositions([]string{"AAPL", "GOOG"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	positions, _ := uc.GetPositions()
	if len(positions) != 1 || positions[0].Symbol != "MSFT" {
		t.Fatalf("expected only MSFT to remain, got %+v", positions)
	}
}

func TestRemovePositionsJoinsErrorsButDeletesTheRest(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2024-01-01"})

	err := uc.RemovePositions([]string{"", "AAPL"})
	if err == nil {
		t.Fatal("expected an error for the empty symbol")
	}
	positions, _ := uc.GetPositions()
	if len(positions) != 0 {
		t.Fatalf("expected AAPL to still be deleted despite the other error, got %+v", positions)
	}
}

func TestRecordTransactionAssignsAnID(t *testing.T) {
	uc := NewUseCase()
	tx := portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"}
	if err := uc.RecordTransaction(tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uc.Transactions) != 1 || uc.Transactions[0].ID == "" {
		t.Fatalf("expected a non-empty generated id, got %+v", uc.Transactions)
	}
}

func TestGetTransactionsReturnsRecorded(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})

	transactions, err := uc.GetTransactions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(transactions) != 1 || transactions[0].Symbol != "AAPL" {
		t.Fatalf("expected one AAPL transaction, got %+v", transactions)
	}
}

// TestRemoveTransactionRecomputesPosition proves the position is rebuilt from the remaining
// transactions, not patched in place: removing the middle SELL of a BUY/SELL/BUY sequence must
// produce the same weighted-average cost as if that SELL had never happened, not whatever an
// incremental "undo" of the SELL's quantity delta would leave behind.
func TestRemoveTransactionRecomputesPosition(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "SELL", Quantity: 4, Price: 120, Timestamp: "2026-01-02T00:00:00Z"})
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 5, Price: 130, Timestamp: "2026-01-03T00:00:00Z"})

	sellID := uc.Transactions[1].ID
	if err := uc.RemoveTransaction(sellID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(uc.Transactions) != 2 {
		t.Fatalf("expected the SELL to be gone from history, got %+v", uc.Transactions)
	}
	positions, _ := uc.GetPositions()
	if len(positions) != 1 {
		t.Fatalf("expected one AAPL position, got %+v", positions)
	}
	wantQuantity, wantAvgCost := 15.0, (100*10.0+130*5.0)/15.0
	if positions[0].Quantity != wantQuantity {
		t.Fatalf("expected quantity %v (as if the SELL never happened), got %v", wantQuantity, positions[0].Quantity)
	}
	if positions[0].AverageCost != wantAvgCost {
		t.Fatalf("expected average cost %v, got %v", wantAvgCost, positions[0].AverageCost)
	}
}

func TestRemoveTransactionForUnknownIDIsANoop(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})

	if err := uc.RemoveTransaction("does-not-exist"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uc.Transactions) != 1 {
		t.Fatalf("expected the existing transaction to be untouched, got %+v", uc.Transactions)
	}
}

func TestRemoveTransactionRejectsEmptyID(t *testing.T) {
	uc := NewUseCase()
	if err := uc.RemoveTransaction(""); err == nil {
		t.Fatal("expected error for empty id")
	}
}

// TestRemoveTransactionsDeletesAcrossSymbols proves a bulk delete recomputes every affected
// symbol correctly, not just the first one — each id goes through the same RemoveTransaction
// path individually, so a batch spanning AAPL and MSFT must leave both positions as if only
// their own remaining transactions had ever been recorded.
func TestRemoveTransactionsDeletesAcrossSymbols(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 5, Price: 200, Timestamp: "2026-01-02T00:00:00Z"})
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "MSFT", Type: "BUY", Quantity: 3, Price: 300, Timestamp: "2026-01-03T00:00:00Z"})

	aaplSecondBuyID := uc.Transactions[1].ID
	msftBuyID := uc.Transactions[2].ID

	if err := uc.RemoveTransactions([]string{aaplSecondBuyID, msftBuyID}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(uc.Transactions) != 1 {
		t.Fatalf("expected only the first AAPL BUY left, got %+v", uc.Transactions)
	}
	positions, _ := uc.GetPositions()
	if len(positions) != 1 {
		// recomputePosition rebuilding a symbol down to zero quantity is dropped entirely (see
		// applyPositionUpdate), the same rule a real SELL-to-zero follows — MSFT's only
		// transaction is gone, so its position row shouldn't linger either.
		t.Fatalf("expected only AAPL left (MSFT's last transaction was removed), got %+v", positions)
	}
	aapl := positions[findPositionIndex(positions, "AAPL")]
	if aapl.Quantity != 10 || aapl.AverageCost != 100 {
		t.Fatalf("expected AAPL 10@100 (as if the second BUY never happened), got %+v", aapl)
	}
}

// TestRemoveTransactionsJoinsErrorsButDeletesTheRest proves one bad id in a batch doesn't block
// the others from being removed.
func TestRemoveTransactionsJoinsErrorsButDeletesTheRest(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})
	realID := uc.Transactions[0].ID

	err := uc.RemoveTransactions([]string{"", realID})
	if err == nil {
		t.Fatal("expected an error for the empty id")
	}
	if len(uc.Transactions) != 0 {
		t.Fatalf("expected the real id to still be deleted despite the other error, got %+v", uc.Transactions)
	}
}

func TestRemoveTransactionsEmptyListIsANoop(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})
	if err := uc.RemoveTransactions(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uc.Transactions) != 1 {
		t.Fatalf("expected the existing transaction untouched, got %+v", uc.Transactions)
	}
}

// fakeRepository is a real, deterministic in-memory Repository — matching stubExchangeRates'
// convention above, not a mock of business logic. Positions/transactions are keyed by
// portfolioID, matching how TimescaleDB actually scopes them.
type fakeRepository struct {
	portfolios   []portfolio.Portfolio
	nextID       int
	positions    map[string][]portfolio.Position
	transactions map[string][]portfolio.Transaction
}

func (f *fakeRepository) newID() string {
	f.nextID++
	return fmt.Sprintf("id-%d", f.nextID)
}

func (f *fakeRepository) ListPortfolios() ([]portfolio.Portfolio, error) {
	if len(f.portfolios) == 0 {
		if _, err := f.CreatePortfolio("Main Portfolio"); err != nil {
			return nil, err
		}
	}
	return f.portfolios, nil
}

func (f *fakeRepository) CreatePortfolio(name string) (portfolio.Portfolio, error) {
	p := portfolio.Portfolio{ID: f.newID(), Name: name}
	f.portfolios = append(f.portfolios, p)
	return p, nil
}

func (f *fakeRepository) RenamePortfolio(id, name string) error {
	for i := range f.portfolios {
		if f.portfolios[i].ID == id {
			f.portfolios[i].Name = name
			return nil
		}
	}
	return nil
}

func (f *fakeRepository) DeletePortfolio(id string) error {
	kept := f.portfolios[:0:0]
	for _, p := range f.portfolios {
		if p.ID != id {
			kept = append(kept, p)
		}
	}
	f.portfolios = kept
	delete(f.positions, id)
	delete(f.transactions, id)
	return nil
}

func (f *fakeRepository) GetPositions(portfolioID string) ([]portfolio.Position, error) {
	return f.positions[portfolioID], nil
}

func (f *fakeRepository) SavePositions(portfolioID string, positions []portfolio.Position) error {
	if f.positions == nil {
		f.positions = make(map[string][]portfolio.Position)
	}
	f.positions[portfolioID] = positions
	return nil
}

func (f *fakeRepository) DeletePosition(portfolioID, symbol string) error {
	kept := f.positions[portfolioID][:0:0]
	for _, p := range f.positions[portfolioID] {
		if p.Symbol != symbol {
			kept = append(kept, p)
		}
	}
	f.positions[portfolioID] = kept
	return nil
}

func (f *fakeRepository) GetAllPositions() ([]portfolio.Position, error) {
	var all []portfolio.Position
	for _, positions := range f.positions {
		all = append(all, positions...)
	}
	return all, nil
}

func (f *fakeRepository) SaveTransaction(portfolioID string, tx portfolio.Transaction) (string, error) {
	tx.ID = f.newID()
	if f.transactions == nil {
		f.transactions = make(map[string][]portfolio.Transaction)
	}
	f.transactions[portfolioID] = append(f.transactions[portfolioID], tx)
	return tx.ID, nil
}

func (f *fakeRepository) GetTransactions(portfolioID string) ([]portfolio.Transaction, error) {
	return f.transactions[portfolioID], nil
}

func (f *fakeRepository) DeleteTransaction(id string) error {
	for portfolioID, txs := range f.transactions {
		kept := txs[:0:0]
		for _, tx := range txs {
			if tx.ID != id {
				kept = append(kept, tx)
			}
		}
		f.transactions[portfolioID] = kept
	}
	return nil
}

func TestNewUseCaseWithRepositoryBootstrapsOnePortfolio(t *testing.T) {
	uc, err := NewUseCaseWithRepository(&fakeRepository{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uc.Portfolios) != 1 || uc.Portfolios[0].Name != "Main Portfolio" {
		t.Fatalf("expected one bootstrapped portfolio, got %+v", uc.Portfolios)
	}
	if uc.ActivePortfolioID != uc.Portfolios[0].ID {
		t.Fatalf("expected the bootstrapped portfolio to be active, got %q", uc.ActivePortfolioID)
	}
}

func TestSwitchPortfolioScopesPositionsIndependently(t *testing.T) {
	uc, err := NewUseCaseWithRepository(&fakeRepository{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mainID := uc.ActivePortfolioID
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})

	second, err := uc.CreatePortfolio("Speculative")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := uc.SwitchPortfolio(second.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uc.Positions) != 0 {
		t.Fatalf("expected the new portfolio to start empty, got %+v", uc.Positions)
	}
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "MSFT", Type: "BUY", Quantity: 3, Price: 50, Timestamp: "2026-01-01T00:00:00Z"})

	if err := uc.SwitchPortfolio(mainID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uc.Positions) != 1 || uc.Positions[0].Symbol != "AAPL" {
		t.Fatalf("expected switching back to restore AAPL only, got %+v", uc.Positions)
	}

	all, err := uc.GetAllPositions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected GetAllPositions to combine both portfolios, got %+v", all)
	}
}

func TestCreatePortfolioEnforcesMaxPortfolios(t *testing.T) {
	uc, err := NewUseCaseWithRepository(&fakeRepository{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 1; i < MaxPortfolios; i++ {
		if _, err := uc.CreatePortfolio("Extra"); err != nil {
			t.Fatalf("unexpected error creating portfolio %d: %v", i, err)
		}
	}
	if _, err := uc.CreatePortfolio("One Too Many"); err == nil {
		t.Fatal("expected creating beyond MaxPortfolios to fail")
	}
}

func TestDeletePortfolioRefusesToDeleteTheLastOne(t *testing.T) {
	uc, err := NewUseCaseWithRepository(&fakeRepository{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := uc.DeletePortfolio(uc.ActivePortfolioID); err == nil {
		t.Fatal("expected deleting the only remaining portfolio to fail")
	}
}
