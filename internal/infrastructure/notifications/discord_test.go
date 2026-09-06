package notifications

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// The embed path is what alerts use, and Discord silently renders nothing useful if the payload
// shape is wrong, so assert the actual JSON rather than only that a request happened.
func TestSendEmbedPostsDiscordEmbedPayload(t *testing.T) {
	var payload struct {
		Content string `json:"content"`
		Embeds  []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			Color       int    `json:"color"`
			Fields      []struct {
				Name   string `json:"name"`
				Value  string `json:"value"`
				Inline bool   `json:"inline"`
			} `json:"fields"`
			Footer *struct {
				Text string `json:"text"`
			} `json:"footer"`
		} `json:"embeds"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("expected a JSON content type, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("payload is not valid JSON: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewDiscordClient(server.URL)
	err := client.SendEmbed(context.Background(), Embed{
		Title:       "ORCL — BUY",
		URL:         "https://finance.yahoo.com/quote/ORCL/",
		Description: "• Momentum signal is favorable\n• ML signal supportive",
		Color:       0x2ECC71,
		Fields: []EmbedField{
			{Name: "Confidence", Value: "67%", Inline: true},
			{Name: "Score", Value: "0.834", Inline: true},
		},
		Footer: &EmbedFooter{Text: "Atlas"},
	})
	if err != nil {
		t.Fatalf("expected send to succeed, got %v", err)
	}

	if len(payload.Embeds) != 1 {
		t.Fatalf("expected exactly one embed, got %d", len(payload.Embeds))
	}
	embed := payload.Embeds[0]
	if embed.Title != "ORCL — BUY" {
		t.Errorf("title = %q", embed.Title)
	}
	if embed.URL != "https://finance.yahoo.com/quote/ORCL/" {
		t.Errorf("url = %q", embed.URL)
	}
	// The newline is the whole point of the rewrite: reasons are a list, not a run-on sentence.
	if !strings.Contains(embed.Description, "\n") {
		t.Errorf("expected the description to carry its own line breaks, got %q", embed.Description)
	}
	if embed.Color != 0x2ECC71 {
		t.Errorf("color = %d, want %d", embed.Color, 0x2ECC71)
	}
	if len(embed.Fields) != 2 || !embed.Fields[0].Inline {
		t.Errorf("expected two inline fields, got %+v", embed.Fields)
	}
	if embed.Footer == nil || embed.Footer.Text != "Atlas" {
		t.Errorf("footer = %+v", embed.Footer)
	}
	// content and embeds are alternatives; sending both would render the message twice.
	if payload.Content != "" {
		t.Errorf("expected no content alongside the embed, got %q", payload.Content)
	}
}

// An unset colour must be omitted rather than serialized as 0, which Discord renders as black
// instead of falling back to its own default.
func TestSendEmbedOmitsUnsetColor(t *testing.T) {
	var raw string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		raw = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewDiscordClient(server.URL)
	if err := client.SendEmbed(context.Background(), Embed{Title: "WAIT"}); err != nil {
		t.Fatalf("expected send to succeed, got %v", err)
	}
	if strings.Contains(raw, `"color"`) {
		t.Fatalf("expected an unset colour to be omitted, got %s", raw)
	}
}
