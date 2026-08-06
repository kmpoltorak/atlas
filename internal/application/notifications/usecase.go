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
