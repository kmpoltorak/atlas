package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// TelegramClient posts via the Telegram Bot API's sendMessage call. Unlike Discord/Slack's
// webhook URL, Telegram needs a bot token (from @BotFather) plus the target chat's id — there is
// no single-URL webhook equivalent for pushing a message out.
type TelegramClient struct {
	BotToken string
	ChatID   string
	Client   *http.Client
}

func NewTelegramClient(botToken, chatID string) *TelegramClient {
	return &TelegramClient{BotToken: botToken, ChatID: chatID, Client: &http.Client{}}
}

type telegramPayload struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

// SendEmbed has no Telegram equivalent to render as a card, so it flattens Embed into Markdown:
// a bold title, the description, then one "Name: Value" line per field.
func (c *TelegramClient) SendEmbed(ctx context.Context, embed Embed) error {
	var b strings.Builder
	if embed.Title != "" {
		fmt.Fprintf(&b, "*%s*\n", embed.Title)
	}
	if embed.Description != "" {
		fmt.Fprintf(&b, "%s\n", embed.Description)
	}
	for _, f := range embed.Fields {
		fmt.Fprintf(&b, "%s: %s\n", f.Name, f.Value)
	}
	if embed.Footer != nil {
		fmt.Fprintf(&b, "_%s_", embed.Footer.Text)
	}
	return c.post(ctx, strings.TrimSpace(b.String()))
}

func (c *TelegramClient) post(ctx context.Context, text string) error {
	if c.BotToken == "" || c.ChatID == "" {
		return fmt.Errorf("bot token and chat id are required")
	}
	if c.Client == nil {
		c.Client = &http.Client{}
	}
	body, err := json.Marshal(telegramPayload{ChatID: c.ChatID, Text: text, ParseMode: "Markdown"})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.BotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
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
		return fmt.Errorf("unexpected telegram api status: %d", resp.StatusCode)
	}
	return nil
}

// Name identifies this channel in the alert outbox — see Notifier.
func (c *TelegramClient) Name() string { return "telegram" }
