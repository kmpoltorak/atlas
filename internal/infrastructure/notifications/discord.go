package notifications

import (
	"bytes"
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

// Send posts a plain-text message.
func (c *DiscordClient) Send(message string) error {
	return c.post(webhookPayload{Content: message})
}

// SendEmbed posts a single rich embed. Kept separate from Send rather than replacing it: Send is
// still the right shape for anything that is genuinely one line, and collapsing the two would
// force every caller to build an embed to say something simple.
func (c *DiscordClient) SendEmbed(embed Embed) error {
	return c.post(webhookPayload{Embeds: []Embed{embed}})
}

func (c *DiscordClient) post(payload webhookPayload) error {
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

	resp, err := c.Client.Post(c.WebhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected webhook status: %d", resp.StatusCode)
	}
	return nil
}
