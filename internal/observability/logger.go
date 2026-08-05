package observability

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger builds a JSON-structured logger. Level is parsed from the LOG_LEVEL
// configuration value; unrecognized values fall back to info.
func NewLogger(level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	parsedLevel, err := zapcore.ParseLevel(level)
	if err != nil {
		parsedLevel = zapcore.InfoLevel
	}
	cfg.Level = zap.NewAtomicLevelAt(parsedLevel)
	return cfg.Build()
}
