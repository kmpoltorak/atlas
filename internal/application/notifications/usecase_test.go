package notifications

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"atlas/internal/infrastructure/notifications"
)

var errFakeSend = errors.New("fake send failure")

func TestSendRequiresMessageAndClient(t *testing.T) {
	uc := NewUseCase()
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

// fakeNotifier lets a test control per-call success/failure without a real HTTP server.
type fakeNotifier struct {
	sendErr  error
	embedErr error
	sent     int
}

func (f *fakeNotifier) Send(string) error {
	f.sent++
	return f.sendErr
}

func (f *fakeNotifier) SendEmbed(notifications.Embed) error {
	f.sent++
	return f.embedErr
}

func TestSendFansOutAndOneFailureDoesNotBlockAnother(t *testing.T) {
	ok := &fakeNotifier{}
	failing := &fakeNotifier{sendErr: errFakeSend}
	uc := NewUseCase(failing, ok)

	err := uc.Send("hello")
	if err == nil {
		t.Fatal("expected the failing client's error to surface")
	}
	if ok.sent != 1 || failing.sent != 1 {
		t.Fatalf("expected both clients to receive the message, got ok=%d failing=%d", ok.sent, failing.sent)
	}
}
