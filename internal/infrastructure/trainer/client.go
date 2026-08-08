// Package trainer is a thin HTTP client for cmd/trainer's internal-only API — the real trigger
// behind the Account dashboard's "Train new candidate" button (see docker-compose.yml's
// atlas-trainer service and docs/ml.md's "Model selection and retraining" section).
package trainer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Status mirrors cmd/trainer's statusResponse JSON shape.
type Status struct {
	Running    bool   `json:"running"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output,omitempty"`
	Failed     bool   `json:"failed"`
}

// Client talks to a running atlas-trainer instance over the Docker network — never exposed
// outside it (see docker-compose.yml's Scope note on that service).
type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: 5 * time.Second}}
}

// Trigger starts a new training run and returns quickly — the trainer runs training/retrain.sh
// in the background and this only confirms it started, not that it finished. A real error means
// either a run was already in progress or the trainer isn't reachable at all (e.g. the
// atlas-trainer service isn't running).
func (c *Client) Trigger() error {
	resp, err := c.http.Post(c.baseURL+"/train", "application/json", nil)
	if err != nil {
		return fmt.Errorf("reach trainer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("a training run is already in progress")
	}
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("trainer returned unexpected status %d", resp.StatusCode)
	}
	return nil
}

// Delete removes a trained model's .onnx file and .json metadata sidecar. Only the trainer can
// do this: atlas-api's own ./training/models mount is read-only by design (see docker-compose
// .yml), so deleting a stale candidate has to go through the one service with write access to
// that directory.
func (c *Client) Delete(filename string) error {
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/delete?filename="+url.QueryEscape(filename), nil)
	if err != nil {
		return fmt.Errorf("build delete request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("reach trainer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if body.Error != "" {
			return fmt.Errorf("%s", body.Error)
		}
		return fmt.Errorf("trainer returned unexpected status %d", resp.StatusCode)
	}
	return nil
}

// Status fetches the most recent (or currently in-progress) run's state.
func (c *Client) Status() (Status, error) {
	resp, err := c.http.Get(c.baseURL + "/status")
	if err != nil {
		return Status{}, fmt.Errorf("reach trainer: %w", err)
	}
	defer resp.Body.Close()
	var status Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return Status{}, fmt.Errorf("decode trainer status: %w", err)
	}
	return status, nil
}
