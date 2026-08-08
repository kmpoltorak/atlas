package app

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"atlas/internal/domain/alerts"

	"go.uber.org/zap"
)

// AlertPollStatus is the last StartAlertPolling background-loop attempt's outcome (success or
// failure, and when) — read by the Alerts page so a poll failure between page loads is visible
// without needing Grafana. Always reflects the most recent attempt, overwritten every tick;
// this is "is the background loop currently healthy," not an accumulating error log (Prometheus/
// Grafana already keep the full history via Metrics.AlertPollErrors).
type AlertPollStatus struct {
	mu  sync.Mutex
	at  time.Time
	err string
}

// record and Snapshot are both nil-receiver-safe: Application.AlertPoll is only non-nil when
// built via app.New (see app.go); tests that construct an Application struct literal directly,
// without a background poll loop to track, leave it nil, and calling either method on that nil
// *AlertPollStatus should just no-op/report "never polled" rather than panic.
func (s *AlertPollStatus) record(at time.Time, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.at = at
	if err != nil {
		s.err = err.Error()
	} else {
		s.err = ""
	}
}

// Snapshot returns the last recorded poll's time and error message (empty if it succeeded, or
// if no poll has run yet — check at.IsZero() for that case).
func (s *AlertPollStatus) Snapshot() (at time.Time, errMsg string) {
	if s == nil {
		return time.Time{}, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.at, s.err
}

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
// changed since the last poll, so the same signal isn't re-sent every tick. Every non-HOLD
// action (including ACCUMULATE/WAIT) is still persisted and tracked for change-detection, but
// only high-priority actions actually push a Discord notification — see isHighPriorityAction.
func (a *Application) pollAlertsOnce(lastAction map[string]string) {
	result, err := a.Alerts.Evaluate()
	a.AlertPoll.record(time.Now().UTC(), err)
	if err != nil {
		a.Logger.Warn("alert evaluation completed with per-symbol errors", zap.Error(err))
		a.Metrics.AlertPollErrors.Inc()
	}

	for _, alert := range result {
		a.persistRecommendation(alert.Recommendation)
		a.persistIndicatorSnapshot(alert.Recommendation.Symbol, alert.Bars)

		symbol := alert.Recommendation.Symbol
		action := alert.Recommendation.Action
		if lastAction[symbol] == action {
			continue
		}
		lastAction[symbol] = action

		if a.Config.DiscordWebhookURL == "" || !isHighPriorityAction(action) {
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

// isHighPriorityAction reports whether an action should push a Discord notification, per
// docs/decision_engine.md's "Notifications" section: BUY, SELL, and REDUCE only — not
// ACCUMULATE, HOLD, or WAIT. GET /alerts itself is unaffected by this (it surfaces every
// non-HOLD action); this only gates the push notification.
func isHighPriorityAction(action string) bool {
	switch action {
	case "BUY", "SELL", "REDUCE":
		return true
	default:
		return false
	}
}

func formatAlertMessage(alert alerts.Alert) string {
	rec := alert.Recommendation
	reasons := strings.Join(rec.Reason, "; ")
	return fmt.Sprintf("[Atlas] %s: %s (confidence %.0f%%) — %s", rec.Symbol, rec.Action, rec.Confidence*100, reasons)
}
