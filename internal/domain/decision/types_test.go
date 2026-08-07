package decision

import "testing"

func TestDefaultThresholdsAreValid(t *testing.T) {
	if !DefaultThresholds().Valid() {
		t.Fatal("expected DefaultThresholds() to satisfy Valid()")
	}
}

func TestThresholdsValid(t *testing.T) {
	tests := []struct {
		name string
		t    Thresholds
		want bool
	}{
		{"defaults", DefaultThresholds(), true},
		{"out of order bearish/bullish", Thresholds{StrongBullish: 0.6, LeanBullish: 0.7, LeanBearish: 0.4, StrongBearish: 0.2, LowMLConfidence: 0.3}, false},
		{"strong bearish above lean bearish", Thresholds{StrongBullish: 0.75, LeanBullish: 0.55, LeanBearish: 0.45, StrongBearish: 0.5, LowMLConfidence: 0.3}, false},
		{"strong bullish above 1", Thresholds{StrongBullish: 1.2, LeanBullish: 0.55, LeanBearish: 0.45, StrongBearish: 0.25, LowMLConfidence: 0.3}, false},
		{"strong bearish below 0", Thresholds{StrongBullish: 0.75, LeanBullish: 0.55, LeanBearish: 0.45, StrongBearish: -0.1, LowMLConfidence: 0.3}, false},
		{"low ml confidence above 1", Thresholds{StrongBullish: 0.75, LeanBullish: 0.55, LeanBearish: 0.45, StrongBearish: 0.25, LowMLConfidence: 1.5}, false},
		{"all bands collapsed to one point", Thresholds{StrongBullish: 0.5, LeanBullish: 0.5, LeanBearish: 0.5, StrongBearish: 0.5, LowMLConfidence: 0.3}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.t.Valid(); got != tt.want {
				t.Fatalf("Valid() = %v, want %v for %+v", got, tt.want, tt.t)
			}
		})
	}
}
