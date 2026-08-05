package notifications

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendUsesWebhookPostPayload(t *testing.T) {
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		if len(body) == 0 {
			t.Fatal("expected webhook payload to be sent")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewDiscordClient(server.URL)
	if err := client.Send("hello-atlas"); err != nil {
		t.Fatalf("expected send to succeed, got %v", err)
	}
	if !requested {
		t.Fatal("expected webhook server to receive a request")
	}
}
