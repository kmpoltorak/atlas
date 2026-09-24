package config

import (
	"errors"
	"golang.org/x/crypto/bcrypt"
	"os"
	"strconv"
)

type Config struct {
	AuthDisabled     bool
	HTTPAddr         string
	LogLevel         string
	DatabaseHost     string
	DatabasePort     string
	DatabaseName     string
	DatabaseUser     string
	DatabasePassword string
	DatabaseSSLMode  string
	// Connection-pool bounds. database/sql leaves open connections unlimited by default, which
	// is how a burst of slow queries turns into Postgres refusing connections to everything
	// else on the box. 0 means "use persistence.PoolLimits' default".
	DatabaseMaxOpenConns        int
	DatabaseMaxIdleConns        int
	DatabaseConnMaxLifetimeMins int
	RedisHost                   string
	RedisPort                   string
	RedisPassword               string
	RedisTLS                    bool
	DiscordWebhookURL           string
	SlackWebhookURL             string
	TelegramBotToken            string
	TelegramChatID              string
	CollectorWindow             int
	AlertPollSeconds            int
	// DerivedRetentionDays bounds how long recomputable history (indicators, recommendations,
	// predictions) is kept. 0 — the default — keeps everything: automatic deletion of data the
	// operator can still look at is opt-in, never something an upgrade turns on silently.
	DerivedRetentionDays int
	MLEnabled            bool
	ModelPath            string
	RecoveryModelPath    string
	LibOnnxRuntimePath   string
	MLModelsDir          string
	TrainerURL           string
	AdminUsername        string
	AdminPasswordHash    string
	CookieSecure         bool
	// RequireBackendTLS makes encrypted, authenticated links to Postgres and Redis a startup
	// requirement — see Validate.
	RequireBackendTLS bool
	PrometheusURL     string
	AlertmanagerURL   string
	GrafanaURL        string
}

func Load() Config {
	return Config{
		AuthDisabled:     envOrDefault("AUTH_DISABLED", "false") == "true",
		HTTPAddr:         ":" + envOrDefault("SERVER_PORT", "8080"),
		LogLevel:         envOrDefault("LOG_LEVEL", "info"),
		DatabaseHost:     envOrDefault("DATABASE_HOST", "timescaledb"),
		DatabasePort:     envOrDefault("DATABASE_PORT", "5432"),
		DatabaseName:     envOrDefault("DATABASE_NAME", "atlas"),
		DatabaseUser:     envOrDefault("DATABASE_USER", "atlas"),
		DatabasePassword: envOrDefault("DATABASE_PASSWORD", "atlas"),
		// "disable" keeps the docker-compose stack (Postgres on a private compose network with
		// no server certificate) working out of the box. Validate refuses it when
		// REQUIRE_BACKEND_TLS is true — see deploy/configmap.yaml, which sets both.
		DatabaseSSLMode: envOrDefault("DATABASE_SSLMODE", "disable"),
		// 0 defers to persistence.PoolLimits' documented defaults, which is what an unset
		// environment gets. Set these when a real deployment shows connection contention — the
		// defaults are conservative, not measured.
		DatabaseMaxOpenConns:        envOrDefaultInt("DATABASE_MAX_OPEN_CONNS", 0),
		DatabaseMaxIdleConns:        envOrDefaultInt("DATABASE_MAX_IDLE_CONNS", 0),
		DatabaseConnMaxLifetimeMins: envOrDefaultInt("DATABASE_CONN_MAX_LIFETIME_MINUTES", 0),
		RedisHost:                   envOrDefault("REDIS_HOST", "redis"),
		RedisPort:                   envOrDefault("REDIS_PORT", "6379"),
		RedisPassword:               envOrDefault("REDIS_PASSWORD", ""),
		RedisTLS:                    envOrDefault("REDIS_TLS", "false") == "true",
		DiscordWebhookURL:           envOrDefault("DISCORD_WEBHOOK_URL", ""),
		SlackWebhookURL:             envOrDefault("SLACK_WEBHOOK_URL", ""),
		TelegramBotToken:            envOrDefault("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID:              envOrDefault("TELEGRAM_CHAT_ID", ""),
		// 30 days comfortably covers the 15-bar RSI warm-up (analyzer.ComputeRSI) with room
		// left for the decision/backtesting pipeline. Providers also fall back to 30 when
		// called directly with window<=0 (e.g. in tests) — kept in sync manually since
		// there's no shared constant across the config/providers package boundary.
		CollectorWindow: envOrDefaultInt("COLLECTOR_WINDOW_DAYS", 30),
		// 300s balances near-real-time alerts against provider rate-limit exposure (see
		// providers/yahoo.go's User-Agent workaround for why that's a real concern here).
		// internal/app/alerts_loop.go duplicates this as a fallback for the same reason.
		AlertPollSeconds: envOrDefaultInt("ALERT_POLL_INTERVAL_SECONDS", 300),
		// The alert loop writes one recommendation per watched symbol per poll (HOLD included),
		// so these tables grow without bound at ~288 rows/symbol/day. Set this to e.g. 365 on a
		// long-lived instance: the tables are still queried, restored and backed up in full, and
		// unbounded growth costs disk and query time even now that WriteBackup streams row by row
		// rather than buffering a table at a time.
		DerivedRetentionDays: envOrDefaultInt("DERIVED_RETENTION_DAYS", 0),
		// True by default (unchanged behavior). Set ML_ENABLED=false to make the decision engine
		// score BUY/SELL/HOLD from the technical term alone (RSI/MACD/Bollinger/momentum, see
		// decision.Orchestrator.buildTechnicalScore) — skipping the ML+Portfolio+Risk blend
		// entirely (this is the initial value of the orchestrator's own MLEnabled flag; see
		// decision.Orchestrator.SetMLEnabled, which the Account page toggles at runtime), not just
		// falling back to the RSI-derived ML stub, which still applies even with no model file
		// loaded. Model files, if configured, still load and stay usable via /predict and the
		// Account page — this only stops the decision engine from factoring them in.
		MLEnabled: envOrDefault("ML_ENABLED", "true") == "true",
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
		// everything else here. docker-compose.yml runs the atlas-trainer service in the default
		// stack and sets this to reach it; Kubernetes has no trainer — see cmd/trainer.
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
		// Separate from COOKIE_SECURE on purpose. Whether the browser reaches Atlas over HTTPS and
		// whether Atlas reaches its database over TLS are different questions: a Compose stack
		// behind a reverse proxy serves HTTPS while Postgres and Redis sit on a private bridge on
		// the same host, and tying the two made COOKIE_SECURE=true refuse to start there.
		// deploy/configmap.yaml sets this, since cluster traffic crosses nodes.
		RequireBackendTLS: envOrDefault("REQUIRE_BACKEND_TLS", "false") == "true",
		// Unlike TrainerURL, these default to the in-network Compose service names rather than
		// "" — prometheus/alertmanager/grafana are part of every deployment Atlas ships, the
		// trainer only of the Compose one. /health reports each as
		// purely informational (see healthHandler) — set to "" to disable a given check
		// entirely, e.g. in a deployment topology that doesn't run one of these. That is why
		// these read through envOrUnset: envOrDefault treats "" as unset and would put the
		// default straight back.
		PrometheusURL:   envOrUnset("PROMETHEUS_URL", "http://prometheus:9090"),
		AlertmanagerURL: envOrUnset("ALERTMANAGER_URL", "http://alertmanager:9093"),
		GrafanaURL:      envOrUnset("GRAFANA_URL", "http://grafana:3000"),
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// envOrUnset returns fallback only when key is absent from the environment; an explicitly empty
// value stays empty.
func envOrUnset(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
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

// Validate rejects partial credentials and requires explicit opt-in to local no-auth mode.
func (c Config) Validate() error {
	if (c.AdminUsername == "") != (c.AdminPasswordHash == "") {
		return errors.New("ADMIN_USERNAME and ADMIN_PASSWORD_HASH must both be configured")
	}
	if c.AdminUsername == "" {
		if !c.AuthDisabled {
			return errors.New("configure admin credentials or explicitly set AUTH_DISABLED=true for local use")
		}
	} else if _, err := bcrypt.Cost([]byte(c.AdminPasswordHash)); err != nil {
		return errors.New("ADMIN_PASSWORD_HASH must be a valid bcrypt hash")
	}
	if c.CollectorWindow <= 0 || c.CollectorWindow > 730 {
		return errors.New("COLLECTOR_WINDOW_DAYS must be between 1 and 730")
	}
	if c.AlertPollSeconds <= 0 {
		return errors.New("ALERT_POLL_INTERVAL_SECONDS must be positive")
	}
	if c.DerivedRetentionDays < 0 {
		return errors.New("DERIVED_RETENTION_DAYS must be 0 (keep everything) or positive")
	}
	// Opt-in rather than inferred from COOKIE_SECURE: see RequireBackendTLS in Load.
	if c.RequireBackendTLS {
		if c.DatabaseSSLMode == "disable" {
			return errors.New("DATABASE_SSLMODE=disable is not allowed with REQUIRE_BACKEND_TLS=true; set it to require or stricter")
		}
		if c.RedisPassword == "" {
			return errors.New("REDIS_PASSWORD is required with REQUIRE_BACKEND_TLS=true")
		}
		if !c.RedisTLS {
			return errors.New("REDIS_TLS must be true with REQUIRE_BACKEND_TLS=true")
		}
	}
	return nil
}
