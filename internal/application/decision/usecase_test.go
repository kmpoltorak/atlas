package decision

import (
	"testing"

	"atlas/internal/domain/decision"
)

func TestBuildRecommendationSynthesizesActionFromSignals(t *testing.T) {
	uc := NewUseCase()
	rec, err := uc.BuildRecommendation(decision.RecommendationInput{
		Technical: decision.TechnicalScore{
			RSI: 62, EMA: 0.68, MACD: 0.41, ATR: 0.91, Volume: 1.05, Overall: 0.82,
		},
		MLConfidence: 1.0,
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

func TestBuildRecommendationActionMatrix(t *testing.T) {
	uc := NewUseCase()
	tests := []struct {
		name         string
		overall      float64
		hasPosition  bool
		mlConfidence float64
		want         string
	}{
		{"strong bullish, no position -> BUY", 0.85, false, 1.0, "BUY"},
		{"strong bullish, existing position -> ACCUMULATE", 0.85, true, 1.0, "ACCUMULATE"},
		{"strong bearish, existing position -> SELL", 0.10, true, 1.0, "SELL"},
		{"strong bearish, no position, confident -> HOLD", 0.10, false, 1.0, "HOLD"},
		{"strong bearish, no position, unconfident -> WAIT", 0.10, false, 0.1, "WAIT"},
		{"lean bearish, existing position -> REDUCE", 0.35, true, 1.0, "REDUCE"},
		{"lean bearish, no position, unconfident -> WAIT", 0.35, false, 0.1, "WAIT"},
		{"lean bearish, no position, confident -> HOLD", 0.35, false, 1.0, "HOLD"},
		// 0.57 sits inside [LeanBullish, StrongBullish) = [0.55, 0.60). It was 0.60 while
		// StrongBullish was 0.75; that value is now the BUY boundary itself.
		{"lean bullish, unconfident -> WAIT", 0.57, false, 0.1, "WAIT"},
		{"lean bullish, confident -> HOLD", 0.57, true, 1.0, "HOLD"},
		{"neutral, unconfident still HOLD (no lean to distrust)", 0.50, false, 0.1, "HOLD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, err := uc.BuildRecommendation(decision.RecommendationInput{
				Technical:    decision.TechnicalScore{Overall: tt.overall},
				HasPosition:  tt.hasPosition,
				MLConfidence: tt.mlConfidence,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rec.Action != tt.want {
				t.Fatalf("expected action %q, got %q", tt.want, rec.Action)
			}
		})
	}
}

func TestBuildRecommendationConfidenceScalesWithScoreStrength(t *testing.T) {
	uc := NewUseCase()

	strong, err := uc.BuildRecommendation(decision.RecommendationInput{
		Technical: decision.TechnicalScore{Overall: 0.95}, MLConfidence: 1.0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	borderline, err := uc.BuildRecommendation(decision.RecommendationInput{
		Technical: decision.TechnicalScore{Overall: 0.76}, MLConfidence: 1.0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strong.Action != "BUY" || borderline.Action != "BUY" {
		t.Fatalf("expected both to be BUY, got %q and %q", strong.Action, borderline.Action)
	}
	if strong.Confidence <= borderline.Confidence {
		t.Fatalf("expected a stronger score to report higher confidence than a borderline one, got %v <= %v", strong.Confidence, borderline.Confidence)
	}

	mixed, err := uc.BuildRecommendation(decision.RecommendationInput{
		Technical: decision.TechnicalScore{Overall: 0.50}, MLConfidence: 1.0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mixed.Action != "HOLD" {
		t.Fatalf("expected HOLD for a perfectly neutral score, got %q", mixed.Action)
	}
	if mixed.Confidence != 0 {
		t.Fatalf("expected a perfectly neutral score to report zero confidence, got %v", mixed.Confidence)
	}
}

func TestBuildRecommendationWaitReportsRealConfidenceNotAConstant(t *testing.T) {
	uc := NewUseCase()
	rec, err := uc.BuildRecommendation(decision.RecommendationInput{
		Technical:    decision.TechnicalScore{Overall: 0.57}, // inside the lean-bullish band
		MLConfidence: 0.12,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Action != "WAIT" {
		t.Fatalf("expected WAIT, got %q", rec.Action)
	}
	if rec.Confidence != 0.12 {
		t.Fatalf("expected WAIT to report the real ML confidence (0.12), got %v", rec.Confidence)
	}
}
