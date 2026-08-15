package notifications

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"atlas/internal/infrastructure/notifications"
)

var errFakeSend = errors.New("fake send failure")

func TestSendEmbedRequiresContentAndClient(t *testing.T) {
	uc := NewUseCase()
	if _, err := uc.SendEmbed(notifications.Embed{}, nil); err == nil {
		t.Fatal("expected empty embed to be rejected")
	}
	if _, err := uc.SendEmbed(notifications.Embed{Title: "hello"}, nil); err == nil {
		t.Fatal("expected missing client to be rejected")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := notifications.NewDiscordClient(server.URL)
	client.Client = server.Client()
	uc = NewUseCase(client)
	if _, err := uc.SendEmbed(notifications.Embed{Title: "hello"}, nil); err != nil {
		t.Fatalf("expected informational embed to be accepted, got %v", err)
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

func TestSendEmbedFansOutAndOneFailureDoesNotBlockAnother(t *testing.T) {
	ok := &fakeNotifier{}
	failing := &fakeNotifier{embedErr: errFakeSend}
	uc := NewUseCase(failing, ok)

	failed, err := uc.SendEmbed(notifications.Embed{Title: "hello"}, nil)
	if err == nil {
		t.Fatal("expected the failing client's error to surface")
	}
	if ok.sent != 1 || failing.sent != 1 {
		t.Fatalf("expected both clients to receive the message, got ok=%d failing=%d", ok.sent, failing.sent)
	}
	if len(failed) != 1 || failed[0] != failing {
		t.Fatalf("expected only the failing client reported as failed, got %+v", failed)
	}
}

// The alert loop retries only the channels a previous attempt reported as failed (see
// alerts_loop.go's pendingAlert) — SendEmbed must honor an explicit targets list rather than
// always fanning out to every configured client, or a channel that already delivered would get
// the same alert again.
func TestSendEmbedOnlyCallsExplicitTargets(t *testing.T) {
	untouched := &fakeNotifier{}
	target := &fakeNotifier{}
	uc := NewUseCase(untouched, target)

	failed, err := uc.SendEmbed(notifications.Embed{Title: "hello"}, []notifications.Notifier{target})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(failed) != 0 {
		t.Fatalf("expected no failures, got %+v", failed)
	}
	if target.sent != 1 {
		t.Fatalf("expected the explicit target to receive the message, got %d", target.sent)
	}
	if untouched.sent != 0 {
		t.Fatalf("expected the client outside targets to be left alone, got %d sends", untouched.sent)
	}
}

func TestSendEmbedEmptyTargetsMeansEveryClient(t *testing.T) {
	first := &fakeNotifier{}
	second := &fakeNotifier{}
	uc := NewUseCase(first, second)

	if _, err := uc.SendEmbed(notifications.Embed{Title: "hello"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.sent != 1 || second.sent != 1 {
		t.Fatalf("expected an empty targets list to fan out to every client, got first=%d second=%d", first.sent, second.sent)
	}
}
