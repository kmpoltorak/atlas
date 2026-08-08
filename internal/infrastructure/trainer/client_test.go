package trainer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientTriggerAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/train" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL).Trigger(); err != nil {
		t.Fatalf("Trigger() with a 202 response returned an error: %v", err)
	}
}

func TestClientTriggerConflictWhenAlreadyRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	err := NewClient(srv.URL).Trigger()
	if err == nil {
		t.Fatal("Trigger() with a 409 response returned nil error, want an 'already in progress' error")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("Trigger() error = %q, want it to mention 'already in progress'", err.Error())
	}
}

func TestClientTriggerUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL).Trigger(); err == nil {
		t.Fatal("Trigger() with a 500 response returned nil error")
	}
}

func TestClientTriggerUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unreachableURL := srv.URL
	srv.Close() // closed before use: nothing is listening at unreachableURL anymore

	if err := NewClient(unreachableURL).Trigger(); err == nil {
		t.Fatal("Trigger() against an unreachable trainer returned nil error")
	}
}

func TestClientDeleteSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/delete" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("filename"); got != "atlas-ml-v1.onnx" {
			t.Errorf("filename query param = %q, want %q", got, "atlas-ml-v1.onnx")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL).Delete("atlas-ml-v1.onnx"); err != nil {
		t.Fatalf("Delete() with a 200 response returned an error: %v", err)
	}
}

func TestClientDeleteReturnsServerErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "model is currently active"})
	}))
	defer srv.Close()

	err := NewClient(srv.URL).Delete("atlas-ml-v1.onnx")
	if err == nil {
		t.Fatal("Delete() with an error body returned nil error")
	}
	if err.Error() != "model is currently active" {
		t.Errorf("Delete() error = %q, want the server's own message", err.Error())
	}
}

func TestClientDeleteUnexpectedStatusWithoutErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL).Delete("atlas-ml-v1.onnx"); err == nil {
		t.Fatal("Delete() with a 500 and no JSON body returned nil error")
	}
}

func TestClientPingSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL).Ping(context.Background()); err != nil {
		t.Fatalf("Ping() with a 200 response returned an error: %v", err)
	}
}

func TestClientPingUnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL).Ping(context.Background()); err == nil {
		t.Fatal("Ping() with a 503 response returned nil error")
	}
}

func TestClientStatusDecodesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Status{Running: true, StartedAt: "2026-08-08T10:00:00Z"})
	}))
	defer srv.Close()

	status, err := NewClient(srv.URL).Status()
	if err != nil {
		t.Fatalf("Status() returned an error: %v", err)
	}
	if !status.Running || status.StartedAt != "2026-08-08T10:00:00Z" {
		t.Errorf("Status() = %+v, want Running=true StartedAt=2026-08-08T10:00:00Z", status)
	}
}

func TestClientStatusMalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL).Status(); err == nil {
		t.Fatal("Status() with a non-JSON body returned nil error")
	}
}
