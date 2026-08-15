package notifications

// Notifier is the shape every notification channel implements — DiscordClient, SlackClient,
// TelegramClient. SendEmbed posts a rich card shape (Embed, defined in discord.go), which each
// client translates into whatever its own API expects; all three channels stay fully supported
// and any combination of them can be configured at once (see application/notifications.UseCase).
//
// Deliberately one method: every fan-out path in Atlas posts embeds. Plain-text posting is not
// part of the channel contract because nothing ever needed it polymorphically — the one caller
// that sends plain text (cmd/trainer's training-failure notice) holds a *DiscordClient directly
// and calls its own Send. Add Send back here the day a second channel genuinely needs it.
type Notifier interface {
	SendEmbed(embed Embed) error
}
