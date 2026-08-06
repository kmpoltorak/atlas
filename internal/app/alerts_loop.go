package app

import (
	"fmt"
	"strings"
	"time"

	"atlas/internal/domain/alerts"

	"go.uber.org/zap"
)

// StartAlertPolling runs a background loop that periodically evaluates the watchlist and
// pushes a Discord notification for each symbol whose recommended action changes since the
// last poll. It returns a stop function that must be called to shut the loop down cleanly.
func (a *Application) StartAlertPolling() func() {
	interval := time.Duration(a.Config.AlertPollSeconds) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute // matches config.Load's ALERT_POLL_INTERVAL_SECONDS default
	}
	if a.Config.DiscordWebhookURL == "" {
		a.Logger.Info("discord webhook not configured; alert polling will run without notifications")
	}

	ticker := time.NewTicker(interval)
	done := make(chan struct{})
	lastAction := make(map[string]string)

	go func() {
		for {
			select {
			case <-ticker.C:
				a.pollAlertsOnce(lastAction)
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()

	return func() { close(done) }
}

// pollAlertsOnce evaluates the watchlist once and notifies only for symbols whose action
// changed since the last poll, so the same signal isn't re-sent every tick.
func (a *Application) pollAlertsOnce(lastAction map[string]string) {
	result, err := a.Alerts.Evaluate()
	if err != nil {
		a.Logger.Warn("alert evaluation completed with per-symbol errors", zap.Error(err))
	}

	for _, alert := range result {
		symbol := alert.Recommendation.Symbol
		action := alert.Recommendation.Action
		if lastAction[symbol] == action {
			continue
		}
		lastAction[symbol] = action

		if a.Config.DiscordWebhookURL == "" {
			continue
		}
		if err := a.Notifications.Send(formatAlertMessage(alert)); err != nil {
			a.Logger.Error("failed to send discord alert", zap.String("symbol", symbol), zap.Error(err))
			continue
		}
		a.Metrics.AlertsSent.WithLabelValues(action).Inc()
		a.Logger.Info("discord alert sent", zap.String("symbol", symbol), zap.String("action", action))
	}
}

func formatAlertMessage(alert alerts.Alert) string {
	rec := alert.Recommendation
	reasons := strings.Join(rec.Reason, "; ")
	return fmt.Sprintf("[Atlas] %s: %s (confidence %.0f%%) — %s", rec.Symbol, rec.Action, rec.Confidence*100, reasons)
}
