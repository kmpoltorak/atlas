package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestIsValidModelFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{"correction model", "atlas-ml-v20260808101211.onnx", true},
		{"recovery model", "atlas-ml-recovery-v20260808101211.onnx", true},
		{"empty", "", false},
		{"path traversal", "../../etc/passwd", false},
		{"subdirectory", "sub/atlas-ml-v1.onnx", false},
		{"wrong extension", "atlas-ml-v1.json", false},
		{"missing prefix", "not-a-model.onnx", false},
		{"traversal disguised with prefix", "atlas-ml-../../etc/passwd.onnx", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidModelFilename(tt.filename); got != tt.want {
				t.Errorf("isValidModelFilename(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestFormatTime(t *testing.T) {
	if got := formatTime(time.Time{}); got != "" {
		t.Errorf("formatTime(zero time) = %q, want empty string", got)
	}
	ts := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	if got := formatTime(ts); got != "2026-08-08T10:00:00Z" {
		t.Errorf("formatTime(%v) = %q, want RFC3339", ts, got)
	}
}

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("TRAINER_TEST_VAR", "")
	if got := envOrDefault("TRAINER_TEST_VAR", "fallback"); got != "fallback" {
		t.Errorf("envOrDefault with unset var = %q, want %q", got, "fallback")
	}
	t.Setenv("TRAINER_TEST_VAR", "override")
	if got := envOrDefault("TRAINER_TEST_VAR", "fallback"); got != "override" {
		t.Errorf("envOrDefault with set var = %q, want %q", got, "override")
	}
}

func TestStateSnapshot(t *testing.T) {
	s := &state{}
	got := s.snapshot()
	want := statusResponse{Running: false, ExitCode: 0}
	if got != want {
		t.Errorf("zero-value state.snapshot() = %+v, want %+v", got, want)
	}

	started := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	s.mu.Lock()
	s.running = true
	s.startedAt = started
	s.output = "training output"
	s.mu.Unlock()

	got = s.snapshot()
	if !got.Running || got.StartedAt != "2026-08-08T10:00:00Z" || got.Output != "training output" {
		t.Errorf("state.snapshot() after setting fields = %+v", got)
	}
}

func TestNewTrainerMetricsRegistersEveryCollector(t *testing.T) {
	m := newTrainerMetrics()

	m.runsTotal.WithLabelValues("success").Inc()
	m.runDuration.Observe(42)
	m.lastRunFinished.Set(1786261514)

	if got := testutil.ToFloat64(m.runsTotal.WithLabelValues("success")); got != 1 {
		t.Errorf("runsTotal{outcome=success} after one Inc() = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.lastRunFinished); got != 1786261514 {
		t.Errorf("lastRunFinished = %v, want 1786261514", got)
	}

	rec := httptest.NewRecorder()
	promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, name := range []string{
		"atlas_trainer_runs_total",
		"atlas_trainer_run_duration_seconds",
		"atlas_trainer_last_run_timestamp_seconds",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("scraped output missing metric %q", name)
		}
	}
}

func TestNewTrainerMetricsEachCallIsIndependent(t *testing.T) {
	// Own fresh prometheus.Registry per call, same as observability.NewMetrics — calling it
	// twice must not panic with "duplicate metrics collector registration attempted".
	newTrainerMetrics()
	newTrainerMetrics()
}
