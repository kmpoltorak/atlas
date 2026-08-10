package notifications

import (
	"errors"

	"atlas/internal/infrastructure/notifications"
)

type UseCase struct {
	Client *notifications.DiscordClient
}

func NewUseCase(client *notifications.DiscordClient) *UseCase {
	return &UseCase{Client: client}
}

// Send posts a message and nothing else — deliberately no action/trade path exists here.
// PROMPT.md: "Atlas never executes trades automatically."
func (u *UseCase) Send(message string) error {
	if message == "" {
		return errors.New("message is required")
	}
	if u.Client == nil {
		return errors.New("discord client is not configured")
	}
	return u.Client.Send(message)
}

// SendEmbed posts a rich embed — the alert path uses this so a recommendation arrives as a
// card with a coloured edge and labelled fields instead of one run-on line. Same deliberate
// limitation as Send: this posts and nothing else, no action or trade path exists here.
func (u *UseCase) SendEmbed(embed notifications.Embed) error {
	if embed.Title == "" && embed.Description == "" {
		return errors.New("embed needs a title or description")
	}
	if u.Client == nil {
		return errors.New("discord client is not configured")
	}
	return u.Client.SendEmbed(embed)
}
