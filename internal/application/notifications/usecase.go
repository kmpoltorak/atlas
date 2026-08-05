package notifications

import (
	"errors"

	"atlas/internal/infrastructure/notifications"
)

// UseCase provides the initial notification boundary for decision-support alerts.
type UseCase struct {
	Client *notifications.DiscordClient
}

func NewUseCase(client *notifications.DiscordClient) *UseCase {
	return &UseCase{Client: client}
}

// Send informational Discord notification without any execution path.
func (u *UseCase) Send(message string) error {
	if message == "" {
		return errors.New("message is required")
	}
	if u.Client == nil {
		return errors.New("discord client is not configured")
	}
	return u.Client.Send(message)
}
