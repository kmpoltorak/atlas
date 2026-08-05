package config

import (
	"os"
	"strconv"
)

type Config struct {
	HTTPAddr          string
	LogLevel          string
	DatabaseHost      string
	DatabasePort      string
	DatabaseName      string
	DatabaseUser      string
	DatabasePassword  string
	RedisHost         string
	RedisPort         string
	PrometheusEnabled bool
	MarketProvider    string
	DiscordWebhookURL string
	CollectorWindow   int
	AlertPollSeconds  int
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
		CollectorWindow:   envOrDefaultInt("COLLECTOR_WINDOW_DAYS", 30),
		AlertPollSeconds:  envOrDefaultInt("ALERT_POLL_INTERVAL_SECONDS", 300),
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
