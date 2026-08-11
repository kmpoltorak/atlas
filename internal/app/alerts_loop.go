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
	a.seedLastActions(lastAction)

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

// seedLastActions primes the change-detection map from persisted history, so a restart does not
// re-announce every symbol that is merely continuing to say what it already said.
//
// Without this the map starts empty and the first poll after any restart treats the entire
// actionable watchlist as brand new. That was invisible while actionable signals were rare, and
// became obvious the moment they weren't: four deploys on 2026-08-10 pushed the same seventeen
// BUY tickers four times over, none of which had actually changed action — ORCL had read BUY
// continuously since 15:22.
//
// The seed comes from the recommendations log rather than from a record of what was notified.
// Those are not the same claim, and the difference is worth stating: this restores "what did the
// engine last say about this symbol", which is exactly what lastAction means during a poll, but
// it cannot know whether the corresponding Discord message actually left the building. A message
// that failed to send before a restart will not be retried after one. Tracking delivery properly
// would need its own table; for a single-user tool the failure mode (one missed alert, logged as
// an error when it happened) does not justify that, and pollAlertsOnce below at least no longer
// marks a failed send as delivered within a single process lifetime.
//
// Best-effort by design: no storage, or a failed query, simply leaves the map empty and restores
// the previous behaviour rather than stopping the loop from running at all.
func (a *Application) seedLastActions(lastAction map[string]string) {
	if a.Storage == nil {
		return
	}
	streaks, err := a.Storage.CurrentActionStreaks()
	if err != nil {
		a.Logger.Warn("could not seed alert change-detection from history; "+
			"the first poll may re-announce actions that have not changed", zap.Error(err))
		return
	}
	for symbol, streak := range streaks {
		lastAction[symbol] = streak.Action
	}
	a.Logger.Info("seeded alert change-detection from persisted history", zap.Int("symbols", len(streaks)))
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
	a.refreshWatchlistQuotes()

	for _, alert := range result {
		a.persistRecommendation(alert.Recommendation)
		a.persistIndicatorSnapshot(alert.Recommendation.Symbol, alert.Bars)

		symbol := alert.Recommendation.Symbol
		action := alert.Recommendation.Action
		if lastAction[symbol] == action {
			continue
		}

		// Actions that never push anywhere are recorded straight away: they still have to be
		// tracked, or a later change would be measured against a stale baseline.
		if a.Config.DiscordWebhookURL == "" || !isHighPriorityAction(action) {
			lastAction[symbol] = action
			continue
		}

		if err := a.Notifications.SendEmbed(buildAlertEmbed(alert)); err != nil {
			// Deliberately left unrecorded so the next poll retries. Marking it seen here — which
			// is what this loop used to do, by recording before sending — turned a transient
			// Discord failure into an alert the Author never saw and never would.
			a.Logger.Error("failed to send discord alert", zap.String("symbol", symbol), zap.Error(err))
			continue
		}
		lastAction[symbol] = action
		a.Metrics.AlertsSent.WithLabelValues(action).Inc()
		a.Logger.Info("discord alert sent", zap.String("symbol", symbol), zap.String("action", action))
	}
}

// refreshWatchlistQuotes fetches and persists market.Quote (current live price + prior close)
// for every current watchlist symbol — called once per poll cycle (see pollAlertsOnce),
// independent of alert evaluation, so the Watchlist page's "today" column stays current without
// waiting for an hourly bar to close (see providers.dropUnclosedBars). Best-effort per symbol:
// one provider failure just logs and moves on, matching Evaluate's own per-symbol handling.
func (a *Application) refreshWatchlistQuotes() {
	if a.Collector == nil || a.Collector.Provider == nil || a.Storage == nil {
		return
	}
	items, err := a.Watchlist.List()
	if err != nil {
		a.Logger.Warn("could not list watchlist for quote refresh", zap.Error(err))
		return
	}
	for _, item := range items {
		quote, err := a.Collector.Provider.FetchQuote(item.Symbol)
		if err != nil {
			a.Logger.Warn("failed to fetch quote", zap.String("symbol", item.Symbol), zap.Error(err))
			continue
		}
		if err := a.Storage.UpsertSymbolQuote(item.Symbol, quote); err != nil {
			a.Logger.Warn("failed to persist quote", zap.String("symbol", item.Symbol), zap.Error(err))
		}
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
