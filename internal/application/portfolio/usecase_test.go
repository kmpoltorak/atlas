package portfolio

import (
	"errors"
	"testing"

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

// TestMixedCurrencyPositionHasOneCoherentPLNCostBasis proves the scenario Kris explicitly
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
	if len(positions) != 2 {
		t.Fatalf("expected both positions to still be present rows, got %+v", positions)
	}
	aapl := positions[findPositionIndex(positions, "AAPL")]
	if aapl.Quantity != 10 || aapl.AverageCost != 100 {
		t.Fatalf("expected AAPL 10@100 (as if the second BUY never happened), got %+v", aapl)
	}
	msft := positions[findPositionIndex(positions, "MSFT")]
	if msft.Quantity != 0 {
		// recomputePosition zeroes out (rather than removes) a position once its last
		// transaction is gone — the same behavior a single RemoveTransaction already has;
		// RemovePosition is the separate explicit action that actually drops the row.
		t.Fatalf("expected MSFT zeroed out (its only transaction was removed), got %+v", msft)
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
