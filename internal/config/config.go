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
	RecoveryModelPath  string
	LibOnnxRuntimePath string
	MLModelsDir        string
	TrainerURL         string
	AdminUsername      string
	AdminPasswordHash  string
	CookieSecure       bool
	PrometheusURL      string
	AlertmanagerURL    string
	GrafanaURL         string
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
		// All three default to "": absent means "no trained model available", which is a normal,
		// non-fatal startup state (see internal/application/ml) — Atlas falls back to the
		// deterministic RSI-derived stub (correction) / stays 0 (recovery) rather than failing
		// to start. See training/README.md. RecoveryModelPath is a genuinely separate, optional
		// model from ModelPath — either, both, or neither can be loaded independently.
		ModelPath:          envOrDefault("MODEL_PATH", ""),
		RecoveryModelPath:  envOrDefault("RECOVERY_MODEL_PATH", ""),
		LibOnnxRuntimePath: envOrDefault("LIBONNXRUNTIME_PATH", ""),
		// Directory the Account dashboard page scans for selectable trained models (see
		// internal/application/ml.ListModels) — matches the container path docker-compose.yml
		// mounts training/models to read-only. Only used for discovery/display; MODEL_PATH/
		// RECOVERY_MODEL_PATH above (or a durable Account-page selection — migrations/011) still
		// decide which file actually loads.
		MLModelsDir: envOrDefault("ML_MODELS_DIR", "/models"),
		// Empty by default: the Account page's "Train new candidate" button then just explains
		// how to run training/retrain.sh by hand instead, the same non-fatal-optional pattern as
		// everything else here. Only meaningful if the atlas-trainer service is also running
		// (docker-compose.yml's "trainer" profile, off by default) — see cmd/trainer.
		TrainerURL: envOrDefault("TRAINER_URL", ""),
		// Both default to "": absent means auth is disabled and Atlas runs with no login gate at
		// all, exactly as it did before this feature existed — the same non-fatal-optional
		// pattern as ModelPath/LibOnnxRuntimePath above. See auth.UseCase.Enabled and
		// cmd/generate-admin-credentials for how to produce a real hash when deploying somewhere
		// reachable beyond localhost. 2FA is deliberately not configured here at all — it's
		// enabled/disabled from the dashboard after logging in (see auth.UseCase's doc comment
		// and migrations/008's auth_settings table), not a static secret.
		AdminUsername:     envOrDefault("ADMIN_USERNAME", ""),
		AdminPasswordHash: envOrDefault("ADMIN_PASSWORD_HASH", ""),
		// False by default so the session cookie still works over the plain-HTTP local
		// docker-compose stack (no reverse proxy terminating TLS). Set COOKIE_SECURE=true
		// once Atlas is actually served over HTTPS (e.g. behind deploy/ingress.yaml) — a
		// Secure cookie is simply never sent by the browser over an unencrypted connection.
		CookieSecure: envOrDefault("COOKIE_SECURE", "false") == "true",
		// Unlike TrainerURL, these default to the in-network Compose service names rather than
		// "" — prometheus/alertmanager/grafana have no `profiles:` restriction in
		// docker-compose.yml (trainer does: `profiles: ["trainer"]`), so they're always part of
		// the default `docker compose up` stack, not an opt-in sidecar. /health reports each as
		// purely informational (see healthHandler) — set to "" to disable a given check
		// entirely, e.g. in a deployment topology that doesn't run one of these.
		PrometheusURL:   envOrDefault("PROMETHEUS_URL", "http://prometheus:9090"),
		AlertmanagerURL: envOrDefault("ALERTMANAGER_URL", "http://alertmanager:9093"),
		GrafanaURL:      envOrDefault("GRAFANA_URL", "http://grafana:3000"),
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
