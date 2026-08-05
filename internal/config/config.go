package config

import (
	"os"
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
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
