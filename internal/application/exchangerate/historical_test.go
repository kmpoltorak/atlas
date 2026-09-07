package exchangerate

import (
	"testing"
	"time"

	"atlas/internal/domain/currency"
)

// datedProvider answers today's rates and, optionally, a fixed set of dated ones — the shape of
// the production NBP provider, which implements both interfaces.
type datedProvider struct {
	stubProvider
	dated map[string]float64
}

func (p *datedProvider) FetchRateOn(code currency.Code, date time.Time) (float64, error) {
	if rate, ok := p.dated[date.Format("2006-01-02")]; ok {
		return rate, nil
	}
	return 0, currency.ErrNoRateForDate
}

// A trade entered today has no *historical* rate to look up — today's rate is the correct one,
// not a fallback. Reporting it as inexact made portfolio.convertToBase reject every same-day
// foreign-currency trade, because the production provider does implement HistoricalProvider.
func TestToBaseOnReportsTodaysRateAsExact(t *testing.T) {
	uc := NewUseCase(&datedProvider{
		stubProvider: stubProvider{rates: map[currency.Code]float64{currency.USD: 4.0}},
	}, nil)

	converted, exact, err := uc.ToBaseOn(100, currency.USD, time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exact {
		t.Fatal("today's rate is the right rate for a trade dated today, not a fallback")
	}
	if converted != 400 {
		t.Fatalf("expected 400 PLN, got %v", converted)
	}
}

// The same holds for a caller that never set a timestamp at all.
func TestToBaseOnReportsAZeroDateAsExact(t *testing.T) {
	uc := NewUseCase(&datedProvider{
		stubProvider: stubProvider{rates: map[currency.Code]float64{currency.USD: 4.0}},
	}, nil)

	if _, exact, err := uc.ToBaseOn(100, currency.USD, time.Time{}); err != nil || !exact {
		t.Fatalf("expected an exact conversion for an unset date, got exact=%v err=%v", exact, err)
	}
}

func TestToBaseOnUsesTheRatePublishedOnThatDate(t *testing.T) {
	date := time.Now().UTC().AddDate(0, 0, -30).Truncate(24 * time.Hour)
	uc := NewUseCase(&datedProvider{
		stubProvider: stubProvider{rates: map[currency.Code]float64{currency.USD: 4.0}},
		dated:        map[string]float64{date.Format("2006-01-02"): 3.5},
	}, nil)

	converted, exact, err := uc.ToBaseOn(100, currency.USD, date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exact || converted != 350 {
		t.Fatalf("expected an exact 350 PLN at that date's rate, got %v exact=%v", converted, exact)
	}
}

// A past date the provider cannot price falls back to today's rate and must say so — the caller
// refuses to store a cost basis the trade never had.
func TestToBaseOnReportsAHistoricalFallbackAsInexact(t *testing.T) {
	date := time.Now().UTC().AddDate(0, 0, -30)
	uc := NewUseCase(&datedProvider{
		stubProvider: stubProvider{rates: map[currency.Code]float64{currency.USD: 4.0}},
	}, nil)

	_, exact, err := uc.ToBaseOn(100, currency.USD, date)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exact {
		t.Fatal("today's rate standing in for a past date is a fallback, not an exact conversion")
	}
}

// A provider that only knows today cannot price a backdated trade either. This used to report
// exact=true, which is how a March trade could be silently stored at September's rate.
func TestToBaseOnReportsAPastDateAsInexactWithoutAHistoricalProvider(t *testing.T) {
	uc := NewUseCase(&stubProvider{rates: map[currency.Code]float64{currency.USD: 4.0}}, nil)

	_, exact, err := uc.ToBaseOn(100, currency.USD, time.Now().UTC().AddDate(0, 0, -30))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exact {
		t.Fatal("a today-only provider cannot answer a past date exactly")
	}
}
