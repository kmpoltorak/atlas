package portfolio

import (
	"strings"
	"testing"
	"time"

	"atlas/internal/application/exchangerate"
	"atlas/internal/domain/currency"
	"atlas/internal/domain/portfolio"
)

// datedRates is a deterministic HistoricalExchangeRateSource: it knows one date's rate and
// nothing else, which is exactly the shape that catches a caller silently using today's number.
type datedRates struct {
	known    map[string]float64
	todayFX  float64
	askedFor []string
}

func (d *datedRates) ToBase(amount float64, code currency.Code) (float64, error) {
	return amount * d.todayFX, nil
}

func (d *datedRates) ToBaseOn(amount float64, code currency.Code, date time.Time) (float64, bool, error) {
	key := date.Format("2006-01-02")
	d.askedFor = append(d.askedFor, key)
	if rate, ok := d.known[key]; ok {
		return amount * rate, true, nil
	}
	return amount * d.todayFX, false, nil
}

// A trade dated in the past must be converted at that date's rate. Using today's records a cost
// basis the trade never had, and every ROI and risk figure derived from the position inherits it.
func TestRecordTransactionConvertsAtTheTradeDateRate(t *testing.T) {
	rates := &datedRates{known: map[string]float64{"2026-03-10": 3.90}, todayFX: 4.50}
	uc := NewUseCase()
	uc.ExchangeRates = rates

	if err := uc.RecordTransaction(portfolio.Transaction{
		Symbol: "AAPL", Type: "BUY", Quantity: 2, Price: 100, Currency: "USD",
		Timestamp: "2026-03-10T14:00:00Z",
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	if got := uc.Transactions[0].PriceInPLN; got != 390 {
		t.Fatalf("expected the 2026-03-10 rate (3.90) to be used, got %v PLN", got)
	}
	if len(rates.askedFor) == 0 || rates.askedFor[0] != "2026-03-10" {
		t.Fatalf("expected the rate to be looked up for the trade date, got %v", rates.askedFor)
	}
}

// When no dated rate can be had, recording must fail rather than quietly substituting today's —
// a wrong cost basis looks exactly like a right one once it is stored.
func TestRecordTransactionRefusesToGuessAMissingHistoricalRate(t *testing.T) {
	rates := &datedRates{known: map[string]float64{}, todayFX: 4.50}
	uc := NewUseCase()
	uc.ExchangeRates = rates

	err := uc.RecordTransaction(portfolio.Transaction{
		Symbol: "AAPL", Type: "BUY", Quantity: 2, Price: 100, Currency: "USD",
		Timestamp: "2026-03-10T14:00:00Z",
	})
	if err == nil {
		t.Fatal("expected recording to fail rather than fall back to today's rate")
	}
	if !strings.Contains(err.Error(), "2026-03-10") {
		t.Fatalf("the error must name the date that could not be priced, got %v", err)
	}
	if len(uc.Transactions) != 0 {
		t.Fatal("a transaction that could not be priced must not be stored")
	}
}

// A source that only knows today still works — the interface is optional, and the common case
// (a trade entered as it happens) has no historical rate to look up anyway.
func TestRecordTransactionAcceptsATodayOnlyRateSource(t *testing.T) {
	uc := NewUseCase()
	uc.ExchangeRates = stubExchangeRates{rates: map[currency.Code]float64{currency.USD: 4.0}}

	if err := uc.RecordTransaction(portfolio.Transaction{
		Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100, Currency: "USD",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := uc.Transactions[0].PriceInPLN; got != 400 {
		t.Fatalf("expected today's rate to apply, got %v", got)
	}
}

// nbpShapedProvider answers today's rates and a fixed set of dated ones — the shape of the real
// NBP provider, which implements exchangerate.HistoricalProvider.
type nbpShapedProvider struct {
	today map[currency.Code]float64
	dated map[string]float64
}

func (p nbpShapedProvider) FetchRates() (map[currency.Code]float64, error) { return p.today, nil }

func (p nbpShapedProvider) FetchRateOn(code currency.Code, date time.Time) (float64, error) {
	if rate, ok := p.dated[date.Format("2006-01-02")]; ok {
		return rate, nil
	}
	return 0, currency.ErrNoRateForDate
}

// The fakes above each implement one side of the boundary, which is exactly how a mismatch
// between the two survived: exchangerate.UseCase reported a same-day conversion as inexact
// whenever its provider *was* capable of historical rates, and convertToBase rejects inexact
// conversions — so with the production NBP provider no foreign-currency trade entered today could
// be recorded at all. These three cases run the real FX use case through the real portfolio one.
func TestRecordTransactionWithTheRealExchangeRateUseCase(t *testing.T) {
	provider := nbpShapedProvider{
		today: map[currency.Code]float64{currency.USD: 4.0},
		dated: map[string]float64{"2026-03-10": 3.9},
	}

	t.Run("a trade entered today is priced at today's rate", func(t *testing.T) {
		uc := NewUseCase()
		uc.ExchangeRates = exchangerate.NewUseCase(provider, nil)

		if err := uc.RecordTransaction(portfolio.Transaction{
			Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100, Currency: "USD",
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
		if got := uc.Transactions[0].PriceInPLN; got != 400 {
			t.Fatalf("expected 400 PLN at today's rate, got %v", got)
		}
	})

	t.Run("a backdated trade is priced at that date's rate", func(t *testing.T) {
		uc := NewUseCase()
		uc.ExchangeRates = exchangerate.NewUseCase(provider, nil)

		if err := uc.RecordTransaction(portfolio.Transaction{
			Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100, Currency: "USD",
			Timestamp: "2026-03-10T14:00:00Z",
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
		if got := uc.Transactions[0].PriceInPLN; got != 390 {
			t.Fatalf("expected 390 PLN at the 2026-03-10 rate, got %v", got)
		}
	})

	t.Run("a backdated trade with no published rate is refused", func(t *testing.T) {
		uc := NewUseCase()
		uc.ExchangeRates = exchangerate.NewUseCase(provider, nil)

		err := uc.RecordTransaction(portfolio.Transaction{
			Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100, Currency: "USD",
			Timestamp: "2025-01-06T14:00:00Z",
		})
		if err == nil || !strings.Contains(err.Error(), "2025-01-06") {
			t.Fatalf("expected the trade to be refused for its own date, got %v", err)
		}
	})
}

// A trade cannot have happened tomorrow. Accepting one would also mean asking for an exchange
// rate that does not exist yet.
func TestRecordTransactionRejectsAFutureTimestamp(t *testing.T) {
	uc := NewUseCase()

	err := uc.RecordTransaction(portfolio.Transaction{
		Symbol: "AAPL", Type: "BUY", Quantity: 1, Price: 100,
		Timestamp: time.Now().UTC().AddDate(0, 0, 30).Format(time.RFC3339),
	})
	if err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("expected a future-dated transaction to be rejected, got %v", err)
	}
}
