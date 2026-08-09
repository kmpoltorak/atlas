package config

import "testing"

// clearEnv unsets every env var Load reads, so a test can assert on Load's actual defaults
// regardless of what's set in the process running the test suite (a real ADMIN_USERNAME, say,
// in a developer's shell would otherwise silently change what "no env set" means here).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SERVER_PORT", "LOG_LEVEL", "DATABASE_HOST", "DATABASE_PORT", "DATABASE_NAME",
		"DATABASE_USER", "DATABASE_PASSWORD", "REDIS_HOST", "REDIS_PORT", "PROMETHEUS_ENABLED",
		"MARKET_PROVIDER", "DISCORD_WEBHOOK_URL", "COLLECTOR_WINDOW_DAYS",
		"ALERT_POLL_INTERVAL_SECONDS", "MODEL_PATH", "RECOVERY_MODEL_PATH",
		"LIBONNXRUNTIME_PATH", "ML_MODELS_DIR", "TRAINER_URL", "ADMIN_USERNAME",
		"ADMIN_PASSWORD_HASH", "COOKIE_SECURE", "PROMETHEUS_URL", "ALERTMANAGER_URL",
		"GRAFANA_URL",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg := Load()

	want := Config{
		HTTPAddr:           ":8080",
		LogLevel:           "info",
		DatabaseHost:       "timescaledb",
		DatabasePort:       "5432",
		DatabaseName:       "atlas",
		DatabaseUser:       "atlas",
		DatabasePassword:   "atlas",
		RedisHost:          "redis",
		RedisPort:          "6379",
		PrometheusEnabled:  true,
		MarketProvider:     "yahoo",
		DiscordWebhookURL:  "",
		CollectorWindow:    30,
		AlertPollSeconds:   300,
		ModelPath:          "",
		RecoveryModelPath:  "",
		LibOnnxRuntimePath: "",
		MLModelsDir:        "/models",
		TrainerURL:         "",
		AdminUsername:      "",
		AdminPasswordHash:  "",
		CookieSecure:       false,
		PrometheusURL:      "http://prometheus:9090",
		AlertmanagerURL:    "http://alertmanager:9093",
		GrafanaURL:         "http://grafana:3000",
	}
	if cfg != want {
		t.Fatalf("Load() with no env set = %+v, want %+v", cfg, want)
	}
}

func TestLoadReadsEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("DATABASE_HOST", "db.internal")
	t.Setenv("PROMETHEUS_ENABLED", "false")
	t.Setenv("COLLECTOR_WINDOW_DAYS", "45")
	t.Setenv("ALERT_POLL_INTERVAL_SECONDS", "60")
	t.Setenv("ADMIN_USERNAME", "admin")
	t.Setenv("COOKIE_SECURE", "true")

	cfg := Load()

	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":9090")
	}
	if cfg.DatabaseHost != "db.internal" {
		t.Errorf("DatabaseHost = %q, want %q", cfg.DatabaseHost, "db.internal")
	}
	if cfg.PrometheusEnabled {
		t.Errorf("PrometheusEnabled = true, want false")
	}
	if cfg.CollectorWindow != 45 {
		t.Errorf("CollectorWindow = %d, want 45", cfg.CollectorWindow)
	}
	if cfg.AlertPollSeconds != 60 {
		t.Errorf("AlertPollSeconds = %d, want 60", cfg.AlertPollSeconds)
	}
	if cfg.AdminUsername != "admin" {
		t.Errorf("AdminUsername = %q, want %q", cfg.AdminUsername, "admin")
	}
	if !cfg.CookieSecure {
		t.Errorf("CookieSecure = false, want true")
	}
}

func TestEnvOrDefaultIntFallsBackOnUnparseableValue(t *testing.T) {
	clearEnv(t)
	t.Setenv("COLLECTOR_WINDOW_DAYS", "not-a-number")

	cfg := Load()

	if cfg.CollectorWindow != 30 {
		t.Errorf("CollectorWindow with an unparseable env value = %d, want the 30-day default", cfg.CollectorWindow)
	}
}

func TestPrometheusEnabledOnlyTrueOnExactMatch(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROMETHEUS_ENABLED", "yes")

	cfg := Load()

	if cfg.PrometheusEnabled {
		t.Errorf("PrometheusEnabled = true for PROMETHEUS_ENABLED=yes, want false (only the literal \"true\" enables it)")
	}
}
