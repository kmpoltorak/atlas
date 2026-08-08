package observability

import (
	"testing"

	"go.uber.org/zap/zapcore"
)

func TestNewLoggerParsesLevel(t *testing.T) {
	logger, err := NewLogger("debug")
	if err != nil {
		t.Fatalf("NewLogger(\"debug\") returned an error: %v", err)
	}
	if !logger.Core().Enabled(zapcore.DebugLevel) {
		t.Errorf("logger built with level \"debug\" doesn't have DebugLevel enabled")
	}
}

func TestNewLoggerFallsBackToInfoOnUnrecognizedLevel(t *testing.T) {
	logger, err := NewLogger("not-a-real-level")
	if err != nil {
		t.Fatalf("NewLogger with an unrecognized level returned an error: %v", err)
	}
	if logger.Core().Enabled(zapcore.DebugLevel) {
		t.Errorf("logger built with an unrecognized level has DebugLevel enabled, want the info fallback")
	}
	if !logger.Core().Enabled(zapcore.InfoLevel) {
		t.Errorf("logger built with an unrecognized level doesn't have InfoLevel enabled")
	}
}
