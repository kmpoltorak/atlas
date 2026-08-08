// Command trainer exposes a minimal, internal-only HTTP API that runs training/retrain.sh on
// demand — the real trigger behind the Account dashboard page's "Train new candidate" button.
//
// This deliberately lives in its own container (see training/Dockerfile), never inside
// atlas-api's own image: retrain.sh needs the Go toolchain (to `go run
// ./cmd/export-training-data`) and a Python virtualenv (to run training/train.py) at runtime,
// and atlas-api's production image is explicitly not supposed to depend on either — see
// PROMPT.md's "Do not create Python backend services" / "production system must not depend on
// Python runtime" and skills/ml.md. Training a candidate never changes what's live; promoting
// one is still the separate, human, Account-page action from ml.ListModels/SetModel — this
// binary only ever runs training/retrain.sh, nothing more.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"atlas/internal/infrastructure/notifications"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

// state tracks the single most recent (or in-progress) training/retrain.sh run. Only one run is
// ever tracked/allowed at a time — see trainHandler's Running check — since two concurrent
// retrain.sh invocations would both write into the same training/models directory and there is
// no real value in running two at once.
type state struct {
	mu         sync.Mutex
	running    bool
	startedAt  time.Time
	finishedAt time.Time
	exitCode   int
	output     string
	failed     bool
}

// outputLimit bounds how much of retrain.sh's combined stdout+stderr the /status endpoint keeps
// — enough to show the real holdout metrics each run prints, without the response growing
// unbounded across many runs (only the most recent run's output is ever kept).
const outputLimit = 8000

func (s *state) snapshot() statusResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	return statusResponse{
		Running:    s.running,
		StartedAt:  formatTime(s.startedAt),
		FinishedAt: formatTime(s.finishedAt),
		ExitCode:   s.exitCode,
		Output:     s.output,
		Failed:     s.failed,
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

type statusResponse struct {
	Running    bool   `json:"running"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output,omitempty"`
	Failed     bool   `json:"failed"`
}

func main() {
	logger, err := observability.NewLogger(envOrDefault("LOG_LEVEL", "info"))
	if err != nil {
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck

	// training/retrain.sh already posts its own Discord notification on *success*
	// (TRAINING_DISCORD_WEBHOOK_URL, inherited by the child process below since cmd.Env is
	// left unset) — see training/README.md's "Regenerating candidates on a schedule". It can
	// never do the same for a *failure*, though: `set -euo pipefail` makes the script exit
	// before ever reaching that notification code. This client fills exactly that gap — see
	// runRetrain — using the identical env var so there's still only one training-notification
	// webhook concept, not two. Empty by default (non-fatal, like every optional dependency).
	discordClient := notifications.NewDiscordClient(envOrDefault("TRAINING_DISCORD_WEBHOOK_URL", ""))

	s := &state{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.snapshot())
	})
	mux.HandleFunc("/train", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.mu.Lock()
		if s.running {
			s.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]string{"error": "a training run is already in progress"})
			return
		}
		s.running = true
		s.startedAt = time.Now()
		s.finishedAt = time.Time{}
		s.mu.Unlock()

		logger.Info("training run started")
		go runRetrain(s, logger, discordClient)

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	})
	mux.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		filename := r.URL.Query().Get("filename")
		if !isValidModelFilename(filename) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid model filename"})
			return
		}
		modelsDir := filepath.Join(envOrDefault("ATLAS_REPO_ROOT", "/app"), "training", "models")
		onnxPath := filepath.Join(modelsDir, filename)
		jsonPath := strings.TrimSuffix(onnxPath, ".onnx") + ".json"

		var errs []string
		if err := os.Remove(onnxPath); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err.Error())
		}
		if err := os.Remove(jsonPath); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err.Error())
		}
		if len(errs) > 0 {
			logger.Warn("failed to delete model", zap.String("filename", filename), zap.Strings("errors", errs))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": strings.Join(errs, "; ")})
			return
		}
		logger.Info("deleted model", zap.String("filename", filename))
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	})

	addr := ":" + envOrDefault("TRAINER_PORT", "8091")
	logger.Info("atlas trainer listening", zap.String("addr", addr))
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Fatal("trainer server stopped", zap.Error(err))
	}
}

// runRetrain shells out to training/retrain.sh — the identical script a human would run by hand
// or from cron (see training/README.md) — and records its outcome. Errors here are expected,
// real outcomes (e.g. no new history since the last run, a bad DB connection), not programming
// bugs: they're captured in state.failed/output for the Account page to display, not panicked
// on. discord is notified of the outcome (best-effort, never blocks/fails this function) so a
// run triggered from the Account page and then left unattended is still noticed.
func runRetrain(s *state, logger *zap.Logger, discord *notifications.DiscordClient) {
	cmd := exec.Command("training/retrain.sh")
	cmd.Dir = envOrDefault("ATLAS_REPO_ROOT", "/app")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	runErr := cmd.Run()

	s.mu.Lock()
	s.running = false
	s.finishedAt = time.Now()
	output := buf.String()
	if len(output) > outputLimit {
		output = output[len(output)-outputLimit:]
	}
	s.output = output
	if runErr != nil {
		s.failed = true
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			s.exitCode = exitErr.ExitCode()
		} else {
			s.exitCode = -1
		}
	} else {
		s.failed = false
		s.exitCode = 0
	}
	failed, exitCode := s.failed, s.exitCode
	s.mu.Unlock()

	if failed {
		logger.Warn("training run failed", zap.Error(runErr))
		notifyTrainingFailure(discord, logger, exitCode)
		return
	}
	// Success is intentionally not notified here — retrain.sh already did it (see discord's
	// doc comment above) with better information (which model files were actually written)
	// than this process has any visibility into.
	logger.Info("training run finished")
}

// notifyTrainingFailure is best-effort: a delivery failure (or no webhook configured at all) is
// logged and otherwise ignored, never surfaced to the training run itself — see
// notifications.DiscordClient.Send's doc comment for the same non-fatal stance used everywhere
// else in Atlas.
func notifyTrainingFailure(discord *notifications.DiscordClient, logger *zap.Logger, exitCode int) {
	if discord.WebhookURL == "" {
		return
	}
	message := fmt.Sprintf("[Atlas trainer] Training run FAILED (exit code %d). Check the Account dashboard page for the full log.", exitCode)
	if err := discord.Send(message); err != nil {
		logger.Warn("failed to send training failure to discord", zap.Error(err))
	}
}

// isValidModelFilename rejects anything that isn't a flat, well-formed model filename
// (training/train.py's atlas-ml[-recovery]-<version>.onnx naming) — no path separators, no "..",
// so /delete can't be used to remove or traverse to an arbitrary file outside training/models.
func isValidModelFilename(filename string) bool {
	if filename == "" || filename != filepath.Base(filename) || strings.Contains(filename, "..") {
		return false
	}
	if !strings.HasSuffix(filename, ".onnx") {
		return false
	}
	return strings.HasPrefix(filename, "atlas-ml-")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
