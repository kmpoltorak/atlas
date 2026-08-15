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

// SendEmbed posts a rich embed to targets, or every configured channel when targets is empty —
// the alert path uses this so a recommendation arrives as a card with a coloured edge and
// labelled fields instead of one run-on line. No action or trade path exists here, only posting.
//
// Returns the subset of targets that failed, so a caller tracking per-symbol delivery state (see
// alerts_loop.go's pendingAlert) can retry only those next time instead of re-sending to channels
// that already succeeded — with multiple channels configured, a plain "did the whole send fail"
// result would otherwise make a retry re-deliver to a channel that already got it.
func (u *UseCase) SendEmbed(embed notifications.Embed, targets []notifications.Notifier) ([]notifications.Notifier, error) {
	if embed.Title == "" && embed.Description == "" {
		return nil, errors.New("embed needs a title or description")
	}
	if len(targets) == 0 {
		targets = u.Clients
	}
	if len(targets) == 0 {
		return nil, errors.New("no notification channel is configured")
	}
	var failed []notifications.Notifier
	var errs []error
	for _, c := range targets {
		if err := c.SendEmbed(embed); err != nil {
			failed = append(failed, c)
			errs = append(errs, err)
		}
	}
	return failed, errors.Join(errs...)
}
