package config

import (
	"os"
	"testing"
)

// clearEnv unsets every env var Load reads, so a test can assert on Load's actual defaults
// regardless of what's set in the process running the test suite (a real DATABASE_HOST, say,
// in a developer's shell would otherwise silently change what "no env set" means here).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SERVER_PORT", "DATABASE_SSLMODE", "REDIS_PASSWORD", "REDIS_TLS", "LOG_LEVEL", "DATABASE_HOST", "DATABASE_PORT", "DATABASE_NAME",
		"DATABASE_USER", "DATABASE_PASSWORD", "REDIS_HOST", "REDIS_PORT",
		"DISCORD_WEBHOOK_URL", "COLLECTOR_WINDOW_DAYS",
		"DERIVED_RETENTION_DAYS", "ALERT_POLL_INTERVAL_SECONDS", "ML_ENABLED", "MODEL_PATH", "RECOVERY_MODEL_PATH",
		"LIBONNXRUNTIME_PATH", "ML_MODELS_DIR", "TRAINER_URL", "PROMETHEUS_URL", "ALERTMANAGER_URL",
		"GRAFANA_URL",
	} {
		t.Setenv(key, "")
	}
	// Unset rather than empty: these three distinguish the two (see envOrUnset).
	for _, key := range []string{"PROMETHEUS_URL", "ALERTMANAGER_URL", "GRAFANA_URL"} {
		unsetEnv(t, key)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "") // registers restoration of the original value
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
}

// Setting one of these to "" is documented as disabling that /health check. envOrDefault put
// the default back instead, so the documented switch did nothing.
func TestEmptyMonitoringURLDisablesTheCheck(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROMETHEUS_URL", "")
	t.Setenv("ALERTMANAGER_URL", "")
	t.Setenv("GRAFANA_URL", "")
	cfg := Load()
	if cfg.PrometheusURL != "" || cfg.AlertmanagerURL != "" || cfg.GrafanaURL != "" {
		t.Fatalf("explicitly empty URLs must stay empty, got %q %q %q", cfg.PrometheusURL, cfg.AlertmanagerURL, cfg.GrafanaURL)
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
		DatabaseSSLMode:    "disable",
		RedisHost:          "redis",
		RedisPort:          "6379",
		DiscordWebhookURL:  "",
		CollectorWindow:    30,
		AlertPollSeconds:   300,
		MLEnabled:          true,
		ModelPath:          "",
		RecoveryModelPath:  "",
		LibOnnxRuntimePath: "",
		MLModelsDir:        "/models",
		TrainerURL:         "",
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
	t.Setenv("COLLECTOR_WINDOW_DAYS", "45")
	t.Setenv("ALERT_POLL_INTERVAL_SECONDS", "60")
	t.Setenv("ML_ENABLED", "false")

	cfg := Load()

	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":9090")
	}
	if cfg.DatabaseHost != "db.internal" {
		t.Errorf("DatabaseHost = %q, want %q", cfg.DatabaseHost, "db.internal")
	}
	if cfg.CollectorWindow != 45 {
		t.Errorf("CollectorWindow = %d, want 45", cfg.CollectorWindow)
	}
	if cfg.AlertPollSeconds != 60 {
		t.Errorf("AlertPollSeconds = %d, want 60", cfg.AlertPollSeconds)
	}
	if cfg.MLEnabled {
		t.Errorf("MLEnabled = true, want false")
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

// The boolean flags Load parses all use the same `== "true"` rule, so anything but that exact
// literal reads as false rather than being coerced. Asserted on ML_ENABLED as the representative
// case — it's the one whose value actually changes engine behavior.
func TestBoolFlagOnlyTrueOnExactMatch(t *testing.T) {
	clearEnv(t)
	t.Setenv("ML_ENABLED", "yes")

	cfg := Load()

	if cfg.MLEnabled {
		t.Errorf("MLEnabled = true for ML_ENABLED=yes, want false (only the literal \"true\" enables it)")
	}
}

// validBase is a Config that Validate accepts, so each case below can change exactly one field
// and attribute the resulting error to it.
func validBase() Config {
	return Config{CollectorWindow: 30, AlertPollSeconds: 300}
}

func TestValidateRejectsNonsenseSettings(t *testing.T) {
	cases := map[string]func(*Config){
		"nonsense collector window":  func(c *Config) { c.CollectorWindow = 0 },
		"oversized collector window": func(c *Config) { c.CollectorWindow = 731 },
		"nonsense poll interval":     func(c *Config) { c.AlertPollSeconds = -1 },
		"negative retention":         func(c *Config) { c.DerivedRetentionDays = -1 },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validBase()
			breakIt(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected %s to be rejected", name)
			}
		})
	}
}

// No credentials exist to configure: an unset environment is a valid, complete configuration.
func TestValidateAcceptsTheDefaults(t *testing.T) {
	clearEnv(t)
	if err := Load().Validate(); err != nil {
		t.Fatalf("the default configuration must validate: %v", err)
	}
}
