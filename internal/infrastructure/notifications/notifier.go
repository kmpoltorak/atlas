package notifications

// Notifier is the shape every notification channel implements — DiscordClient, SlackClient,
// TelegramClient. Send posts plain text; SendEmbed posts the same rich card shape (Embed, defined
// in discord.go), which each client translates into whatever its own API expects.
type Notifier interface {
	Send(message string) error
	SendEmbed(embed Embed) error
}
