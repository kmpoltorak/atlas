package notifications

import (
	"bytes"
	"fmt"
	"net/http"
)

// DiscordClient maintains the minimal webhook transport boundary for informational notification delivery.
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

// Send posts a Discord informational notification to the configured webhook URL.
func (c *DiscordClient) Send(message string) error {
	if c.WebhookURL == "" {
		return fmt.Errorf("webhook url is required")
	}
	if c.Client == nil {
		c.Client = &http.Client{}
	}
	payload := []byte(fmt.Sprintf(`{"content":%q}`, message))
	resp, err := c.Client.Post(c.WebhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected webhook status: %d", resp.StatusCode)
	}
	return nil
}
