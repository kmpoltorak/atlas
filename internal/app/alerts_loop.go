package app

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"atlas/internal/domain/alerts"
	"atlas/internal/domain/market"
	"atlas/internal/infrastructure/notifications"

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
		if err := a.Notifications.SendEmbed(buildAlertEmbed(alert)); err != nil {
			a.Logger.Error("failed to send discord alert", zap.String("symbol", symbol), zap.Error(err))
			continue
		}
		a.Metrics.AlertsSent.WithLabelValues(action).Inc()
		a.Logger.Info("discord alert sent", zap.String("symbol", symbol), zap.String("action", action))
	}
}

// isHighPriorityAction reports whether an action should push a Discord notification, per
// docs/decision_engine.md's "Notifications" section: the four actions that ask you to actually
// do something — BUY, ACCUMULATE, REDUCE, SELL — but not HOLD or WAIT.
//
// ACCUMULATE was originally excluded while REDUCE was included, which left the list asymmetric:
// a partial sell notified, a partial buy didn't. There was no reason for that split, and if
// anything ACCUMULATE is the more actionable of the pair, since by definition it concerns a
// position you already hold. HOLD and WAIT stay out because neither asks for a decision — and
// HOLD in particular is the steady state for most symbols most of the time, so pushing it would
// bury the signals that matter.
//
// GET /alerts is unaffected by this (it surfaces every non-HOLD action, WAIT included); this
// gates only the push notification.
func isHighPriorityAction(action string) bool {
	switch action {
	case "BUY", "ACCUMULATE", "REDUCE", "SELL":
		return true
	default:
		return false
	}
}

// actionColors are the embed's left-edge colour per action, the fastest thing to read in a
// Discord feed: green means the alert wants you to add, red means it wants you to trim or exit.
// Any action without an entry falls through to Discord's default rather than being forced into
// a colour that would imply a direction it doesn't have.
var actionColors = map[string]int{
	"BUY":        0x2ECC71,
	"ACCUMULATE": 0x27AE60,
	"REDUCE":     0xE67E22,
	"SELL":       0xE74C3C,
}

// buildAlertEmbed renders a recommendation as a Discord embed.
//
// This replaces a single flat line that packed symbol, action, confidence, and every rationale
// string into one run-on sentence — readable in isolation, unscannable once a poll fires several
// at once. The embed splits that into the parts you actually read in different orders: colour and
// title for what and how urgent, an inline field row for the numbers, and the rationale as a list
// underneath rather than semicolon-joined prose.
//
// The title links to the symbol's quote page (market.QuoteURL, the same link the dashboard tables
// use), so acting on an alert from a phone is one tap rather than retyping a ticker.
func buildAlertEmbed(alert alerts.Alert) notifications.Embed {
	rec := alert.Recommendation

	reasons := make([]string, 0, len(rec.Reason))
	for _, reason := range rec.Reason {
		reasons = append(reasons, "• "+reason)
	}

	fields := []notifications.EmbedField{
		{Name: "Confidence", Value: fmt.Sprintf("%.0f%%", rec.Confidence*100), Inline: true},
		{Name: "Score", Value: fmt.Sprintf("%.3f", rec.Score), Inline: true},
	}
	// Market condition is only populated when the classifier had enough history to run, and an
	// empty field renders as a blank column rather than being skipped, so it is added only when
	// it actually says something.
	if rec.MarketCondition != "" {
		fields = append(fields, notifications.EmbedField{Name: "Market", Value: rec.MarketCondition, Inline: true})
	}

	return notifications.Embed{
		Title:       fmt.Sprintf("%s — %s", rec.Symbol, rec.Action),
		URL:         market.QuoteURL(rec.Symbol),
		Description: strings.Join(reasons, "\n"),
		Color:       actionColors[rec.Action],
		Fields:      fields,
		Footer:      &notifications.EmbedFooter{Text: "Atlas · decision support only, no trades are executed"},
		Timestamp:   alert.TriggeredAt,
	}
}
