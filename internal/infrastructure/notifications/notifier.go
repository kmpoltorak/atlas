package notifications

import "context"

// Notifier is the shape every notification channel implements — DiscordClient, SlackClient,
// TelegramClient. SendEmbed posts a rich card shape (Embed, defined in discord.go), which each
// client translates into whatever its own API expects; all three channels stay fully supported
// and any combination of them can be configured at once (see application/notifications.UseCase).
//
// Deliberately one method: every fan-out path in Atlas posts embeds. Plain-text posting is not
// part of the channel contract because nothing ever needed it polymorphically — the one caller
// that sends plain text (cmd/trainer's training-failure notice) holds a *DiscordClient directly
// and calls its own Send. Add Send back here the day a second channel genuinely needs it.
// ctx is the caller's deadline and cancellation, not a courtesy: the alert loop sends under the
// leadership term (see app.Leader.Term), so a send already in flight when the lease is lost or
// the process shuts down is abandoned rather than completing on behalf of a lease somebody else
// now holds.
type Notifier interface {
	SendEmbed(ctx context.Context, embed Embed) error
	// Name identifies the channel durably, for the alert outbox: a notification that failed on
	// Slack but succeeded on Discord has to survive a restart knowing which of the two it still
	// owes. An in-memory pointer cannot express that across process lifetimes, so the identity
	// has to be a stable string the database can hold. Names are the channel kind, not the
	// endpoint — there is at most one client per kind (see app.New).
	Name() string
}
