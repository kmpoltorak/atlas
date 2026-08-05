package notifications

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"atlas/internal/infrastructure/notifications"
)

func TestSendRequiresMessageAndClient(t *testing.T) {
	uc := NewUseCase(nil)
	if err := uc.Send(""); err == nil {
		t.Fatal("expected empty message to be rejected")
	}
	if err := uc.Send("hello"); err == nil {
		t.Fatal("expected missing client to be rejected")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := notifications.NewDiscordClient(server.URL)
	client.Client = server.Client()
	uc = NewUseCase(client)
	if err := uc.Send("hello"); err != nil {
		t.Fatalf("expected informational message to be accepted, got %v", err)
	}
}
