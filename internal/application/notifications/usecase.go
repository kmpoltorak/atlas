package notifications

import (
	"errors"

	"atlas/internal/infrastructure/notifications"
)

// UseCase fans a message out to every configured channel (Discord/Slack/Telegram, any
// combination). Each channel is independent: one failing does not stop the others, and the
// caller sees every failure that happened via errors.Join.
type UseCase struct {
	Clients []notifications.Notifier
}

func NewUseCase(clients ...notifications.Notifier) *UseCase {
	return &UseCase{Clients: clients}
}

// Configured reports whether at least one notification channel is set up — callers use this
// instead of checking a specific channel's config (e.g. DISCORD_WEBHOOK_URL) directly, since any
// one of Discord/Slack/Telegram being configured is enough to notify.
func (u *UseCase) Configured() bool {
	return len(u.Clients) > 0
}

// Send posts a message and nothing else — deliberately no action/trade path exists here.
// PROMPT.md: "Atlas never executes trades automatically."
func (u *UseCase) Send(message string) error {
	if message == "" {
		return errors.New("message is required")
	}
	if !u.Configured() {
		return errors.New("no notification channel is configured")
	}
	var errs []error
	for _, c := range u.Clients {
		if err := c.Send(message); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// SendEmbed posts a rich embed — the alert path uses this so a recommendation arrives as a
// card with a coloured edge and labelled fields instead of one run-on line. Same deliberate
// limitation as Send: this posts and nothing else, no action or trade path exists here.
func (u *UseCase) SendEmbed(embed notifications.Embed) error {
	if embed.Title == "" && embed.Description == "" {
		return errors.New("embed needs a title or description")
	}
	if !u.Configured() {
		return errors.New("no notification channel is configured")
	}
	var errs []error
	for _, c := range u.Clients {
		if err := c.SendEmbed(embed); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
