package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type DiscordClient struct {
	WebhookURL string
	Client     *http.Client
}

func NewDiscordClient(webhookURL string) *DiscordClient {
	return &DiscordClient{
		WebhookURL: webhookURL,
		Client:     &http.Client{},
	}
}

// EmbedField is one labelled value in an embed. Inline fields sit side by side in Discord's
// layout (three per row on a desktop client), which is what turns confidence/score/market
// condition into a scannable row instead of three more sentences.
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// Embed is the subset of Discord's embed object Atlas actually uses. Discord renders an embed as
// a card with a coloured left edge, a linked title, and a field grid — the difference between an
// alert you can read at a glance and the single run-on line this replaced.
//
// Color is the 24-bit RGB value Discord expects as a decimal integer; zero is omitted so an
// unset colour falls back to Discord's own default rather than rendering as black.
type Embed struct {
	Title       string       `json:"title,omitempty"`
	URL         string       `json:"url,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"`
}

type EmbedFooter struct {
	Text string `json:"text"`
}

// webhookPayload is what actually goes over the wire. Both fields are optional to Discord
// individually, but a payload with neither is rejected, so every caller here sets one.
type webhookPayload struct {
	Content string  `json:"content,omitempty"`
	Embeds  []Embed `json:"embeds,omitempty"`
}

// Send posts a plain-text message. Not part of the Notifier interface (see notifier.go) — the
// one caller is cmd/trainer's training-failure notice, which holds a *DiscordClient directly and
// has nothing card-shaped to say.
func (c *DiscordClient) Send(message string) error {
	return c.post(context.Background(), webhookPayload{Content: message})
}

// SendEmbed posts a single rich embed — the shape every fan-out path in Atlas uses (see
// application/notifications.UseCase.SendEmbed), across Discord, Slack and Telegram alike.
func (c *DiscordClient) SendEmbed(ctx context.Context, embed Embed) error {
	return c.post(ctx, webhookPayload{Embeds: []Embed{embed}})
}

func (c *DiscordClient) post(ctx context.Context, payload webhookPayload) error {
	if c.WebhookURL == "" {
		return fmt.Errorf("webhook url is required")
	}
	if c.Client == nil {
		c.Client = &http.Client{}
	}

	// Marshalled rather than assembled with fmt: embed descriptions carry newlines and
	// user-visible symbol names, and hand-built JSON is exactly where that goes wrong.
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected webhook status: %d", resp.StatusCode)
	}
	return nil
}

// Name identifies this channel in the alert outbox — see Notifier.
func (c *DiscordClient) Name() string { return "discord" }
