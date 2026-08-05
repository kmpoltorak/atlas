package decision

import (
	"testing"

	"atlas/internal/domain/decision"
)

func TestRecommendReturnsSupportOnlyResponse(t *testing.T) {
	uc := NewUseCase()
	rec, err := uc.Recommend("AAPL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Symbol != "AAPL" {
		t.Fatalf("expected symbol AAPL, got %q", rec.Symbol)
	}
	if rec.Action != "HOLD" {
		t.Fatalf("expected HOLD action, got %q", rec.Action)
	}
}

func TestScoreTechnicalReturnsInputScore(t *testing.T) {
	uc := NewUseCase()
	score, err := uc.ScoreTechnical(decision.TechnicalScore{Overall: 0.74})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0.74 {
		t.Fatalf("expected score 0.74, got %v", score)
	}
}

func TestBuildRecommendationSynthesizesActionFromSignals(t *testing.T) {
	uc := NewUseCase()
	rec, err := uc.BuildRecommendation(decision.TechnicalScore{
		RSI:     62,
		EMA:     0.68,
		MACD:    0.41,
		ATR:     0.91,
		Volume:  1.05,
		Overall: 0.82,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Action != "BUY" {
		t.Fatalf("expected BUY action, got %q", rec.Action)
	}
	if rec.Confidence <= 0.5 {
		t.Fatalf("expected confidence to exceed 0.5, got %v", rec.Confidence)
	}
}
