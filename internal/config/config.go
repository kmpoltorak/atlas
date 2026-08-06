package config

import (
	"os"
	"strconv"
)

type Config struct {
	HTTPAddr           string
	LogLevel           string
	DatabaseHost       string
	DatabasePort       string
	DatabaseName       string
	DatabaseUser       string
	DatabasePassword   string
	RedisHost          string
	RedisPort          string
	PrometheusEnabled  bool
	MarketProvider     string
	DiscordWebhookURL  string
	CollectorWindow    int
	AlertPollSeconds   int
	ModelPath          string
	LibOnnxRuntimePath string
}

func Load() Config {
	return Config{
		HTTPAddr:          ":" + envOrDefault("SERVER_PORT", "8080"),
		LogLevel:          envOrDefault("LOG_LEVEL", "info"),
		DatabaseHost:      envOrDefault("DATABASE_HOST", "timescaledb"),
		DatabasePort:      envOrDefault("DATABASE_PORT", "5432"),
		DatabaseName:      envOrDefault("DATABASE_NAME", "atlas"),
		DatabaseUser:      envOrDefault("DATABASE_USER", "atlas"),
		DatabasePassword:  envOrDefault("DATABASE_PASSWORD", "atlas"),
		RedisHost:         envOrDefault("REDIS_HOST", "redis"),
		RedisPort:         envOrDefault("REDIS_PORT", "6379"),
		PrometheusEnabled: envOrDefault("PROMETHEUS_ENABLED", "true") == "true",
		MarketProvider:    envOrDefault("MARKET_PROVIDER", "yahoo"),
		DiscordWebhookURL: envOrDefault("DISCORD_WEBHOOK_URL", ""),
		// 30 days comfortably covers the 15-bar RSI warm-up (analyzer.ComputeRSI) with room
		// left for the decision/backtesting pipeline. Providers also fall back to 30 when
		// called directly with window<=0 (e.g. in tests) — kept in sync manually since
		// there's no shared constant across the config/providers package boundary.
		CollectorWindow: envOrDefaultInt("COLLECTOR_WINDOW_DAYS", 30),
		// 300s balances near-real-time alerts against provider rate-limit exposure (see
		// providers/yahoo.go's User-Agent workaround for why that's a real concern here).
		// internal/app/alerts_loop.go duplicates this as a fallback for the same reason.
		AlertPollSeconds: envOrDefaultInt("ALERT_POLL_INTERVAL_SECONDS", 300),
		// Both default to "": absent means "no trained model available", which is a normal,
		// non-fatal startup state (see internal/application/ml) — Atlas falls back to the
		// deterministic RSI-derived stub rather than failing to start. See training/README.md.
		ModelPath:          envOrDefault("MODEL_PATH", ""),
		LibOnnxRuntimePath: envOrDefault("LIBONNXRUNTIME_PATH", ""),
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
