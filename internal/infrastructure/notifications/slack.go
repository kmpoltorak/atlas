package notifications

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SlackClient posts to a Slack Incoming Webhook — same one-URL setup as Discord's webhook, no
// bot/OAuth app required.
type SlackClient struct {
	WebhookURL string
	Client     *http.Client
}

func NewSlackClient(webhookURL string) *SlackClient {
	return &SlackClient{WebhookURL: webhookURL, Client: &http.Client{}}
}

type slackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

// slackAttachment is Slack's legacy (but still supported, and far simpler than Block Kit)
// message-attachment shape — a coloured side bar plus a field grid, the same visual job Discord's
// Embed does.
type slackAttachment struct {
	Title  string       `json:"title,omitempty"`
	Text   string       `json:"text,omitempty"`
	Color  string       `json:"color,omitempty"`
	Fields []slackField `json:"fields,omitempty"`
	Footer string       `json:"footer,omitempty"`
	Ts     int64        `json:"ts,omitempty"`
}

type slackPayload struct {
	Text        string            `json:"text,omitempty"`
	Attachments []slackAttachment `json:"attachments,omitempty"`
}

func (c *SlackClient) Send(message string) error {
	return c.post(slackPayload{Text: message})
}

func (c *SlackClient) SendEmbed(embed Embed) error {
	fields := make([]slackField, len(embed.Fields))
	for i, f := range embed.Fields {
		fields[i] = slackField{Title: f.Name, Value: f.Value, Short: f.Inline}
	}
	attachment := slackAttachment{
		Title:  embed.Title,
		Text:   embed.Description,
		Color:  fmt.Sprintf("#%06x", embed.Color),
		Fields: fields,
	}
	if embed.Footer != nil {
		attachment.Footer = embed.Footer.Text
	}
	if embed.Timestamp != "" {
		if ts, err := time.Parse(time.RFC3339, embed.Timestamp); err == nil {
			attachment.Ts = ts.Unix()
		}
	}
	return c.post(slackPayload{Attachments: []slackAttachment{attachment}})
}

func (c *SlackClient) post(payload slackPayload) error {
	if c.WebhookURL == "" {
		return fmt.Errorf("webhook url is required")
	}
	if c.Client == nil {
		c.Client = &http.Client{}
	}
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
