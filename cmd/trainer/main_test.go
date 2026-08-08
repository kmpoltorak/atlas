package main

import (
	"testing"
	"time"
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
