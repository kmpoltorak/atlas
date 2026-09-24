package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	mlUC "atlas/internal/application/ml"

	"atlas/internal/domain/alerts"
	"atlas/internal/domain/currency"
	"atlas/internal/domain/market"
	"atlas/internal/infrastructure/notifications"
	"atlas/internal/infrastructure/persistence"

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
	if !a.Notifications.Configured() {
		a.Logger.Info("no notification channel configured; alert polling will run without notifications")
	}

	ticker := time.NewTicker(interval)
	done := make(chan struct{})

	// Leadership is acquired before anything else starts: every task below is singleton work
	// that must not run once per replica (see Leader).
	releaseLease := a.startLeaderElection(done)

	// Daily, not per poll: pruning is a bulk DELETE whose result changes on the scale of days,
	// and running it every 300s would be pure load. Ticks are independent of the poll ticker so
	// a slow prune can never delay an alert.
	pruneTicker := time.NewTicker(24 * time.Hour)
	// Retries for anything the outbox still holds. Faster than the poll interval because a
	// pending entry is a notification the user has not received yet, and the reason it is
	// pending is usually a transient channel failure that clears in seconds.
	deliverTicker := time.NewTicker(deliveryRetryInterval)
	// Runtime settings are persisted but held in memory (see Orchestrator.SetMLEnabled), so a
	// toggle flipped on the replica serving the Account page never reaches the others. This
	// ticker is deliberately NOT leader-gated: every replica has to converge on the same
	// settings, not just whichever one is polling.
	settingsTicker := time.NewTicker(settingsRefreshInterval)

	// worker is joined before the lease is released. Cancelling the term aborts in-flight I/O, but
	// "aborted" is not "returned": without this the release could still overtake a poll that was
	// on its way out, and the successor would start while this process was still writing.
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		for {
			select {
			case <-ticker.C:
				if a.Leader.IsLeader() {
					a.pollAlertsOnce(a.Leader.Snapshot())
				}
			case <-deliverTicker.C:
				if a.Leader.IsLeader() {
					a.deliverPendingAlerts(a.Leader.Snapshot())
				}
			case <-settingsTicker.C:
				a.refreshRuntimeSettings()
			case <-pruneTicker.C:
				if a.Leader.IsLeader() {
					a.pruneDerivedDataOnce()
				}
			case <-done:
				ticker.Stop()
				pruneTicker.Stop()
				deliverTicker.Stop()
				settingsTicker.Stop()
				return
			}
		}
	}()

	return func() {
		// The order is the whole point. Cancel the term first so anything running stops rather
		// than being waited on; then stop the loop; then wait for it to actually return; only then
		// hand the lease over. Releasing before the worker has exited is what let a former leader
		// keep writing while its successor was already polling.
		a.Leader.endTerm()
		close(done)
		worker.Wait()
		releaseLease()
	}
}

// deliveryRetryInterval is how often undelivered outbox entries are retried.
const deliveryRetryInterval = 30 * time.Second

// outboxClaimTTL is how long a claimed row stays owned by the worker that took it. Long enough to
// cover a slow channel, short enough that a worker dying mid-send does not strand the alert for
// more than one retry cycle.
const outboxClaimTTL = 2 * time.Minute

// maxOutboxBatch bounds one delivery pass, so a long backlog after an outage is worked through
// over several ticks instead of one pass that holds the loop for minutes.
const maxOutboxBatch = 50

// settingsRefreshInterval bounds how long a runtime setting changed on one replica takes to
// reach the others. Half a minute: long enough to be one cheap query, short enough that a
// deliberate change does not appear to have been ignored.
const settingsRefreshInterval = 30 * time.Second

// refreshRuntimeSettings re-reads the durable engine settings so every replica converges on the
// Account page's choices, not just the one that happened to serve the request that made them.
// Runs on every replica, leader or not — a follower serving a recommendation must score it with
// the same engine configuration the leader would.
func (a *Application) refreshRuntimeSettings() {
	if a.Storage == nil || a.Orchestrator == nil {
		return
	}
	a.Orchestrator.SetMLEnabled(resolveMLEnabled(a.Storage, a.Logger, a.Config.MLEnabled))
	a.reconcileModelSelection(mlUC.RoleCorrection)
	a.reconcileModelSelection(mlUC.RoleRecovery)
}

// reconcileModelSelection loads whichever model the database says should be active for role, if
// this replica is not already serving it.
//
// Without this, activating a model changed only the replica that served the click: every other
// one kept serving its previous model, and two replicas answered the same question with
// different models while the UI showed one selection. The Account page's choice is a *desired*
// state; this is the loop that makes each replica's actual state match it.
//
// Silent when already correct — the common case, every 30 seconds. Loud only on a real failure,
// where it keeps the currently loaded model rather than dropping to no model at all: a stale
// model still answers, and an unavailable file is an operator problem, not a reason to degrade.
func (a *Application) reconcileModelSelection(role mlUC.Role) {
	filename, ok, err := a.Storage.GetActiveModelSelection(string(role))
	if err != nil {
		a.Logger.Warn("could not read the active model selection",
			zap.String("role", string(role)), zap.Error(err))
		return
	}
	if !ok {
		return // no durable selection; the env-var default stands
	}

	current := a.ML.CurrentModel()
	if role == mlUC.RoleRecovery {
		current = a.ML.CurrentRecoveryModel()
	}
	if current != nil && current.Filename == filename {
		return
	}

	path := filepath.Join(a.Config.MLModelsDir, filename)
	model, err := mlUC.LoadOnnxModel(path, a.Config.LibOnnxRuntimePath)
	if err != nil {
		a.Logger.Error("the selected ML model could not be loaded on this replica; still serving the previous one",
			zap.String("role", string(role)), zap.String("filename", filename), zap.Error(err))
		return
	}
	var previous *mlUC.OnnxModel
	if role == mlUC.RoleCorrection {
		previous = a.ML.SetModel(model)
	} else {
		previous = a.ML.SetRecoveryModel(model)
	}
	a.Logger.Info("reconciled to the selected ML model",
		zap.String("role", string(role)), zap.String("version", model.Version))
	if previous != nil {
		if err := previous.Close(); err != nil {
			a.Logger.Warn("failed to close the superseded ML model",
				zap.String("role", string(role)), zap.Error(err))
		}
	}
}

// pruneDerivedDataOnce drops indicator/recommendation/prediction rows past the configured
// retention window. Best-effort and non-fatal, like every other background maintenance step:
// a failure here must not stop alerts from being evaluated.
func (a *Application) pruneDerivedDataOnce() {
	if a.Storage == nil || a.Config.DerivedRetentionDays <= 0 {
		return
	}
	maxAge := time.Duration(a.Config.DerivedRetentionDays) * 24 * time.Hour
	deleted, err := a.Storage.PruneDerivedData(maxAge)
	if err != nil {
		a.Logger.Warn("failed to prune derived data", zap.Error(err))
		return
	}
	if deleted > 0 {
		a.Logger.Info("pruned derived data past the retention window",
			zap.Int64("rows", deleted), zap.Int("retention_days", a.Config.DerivedRetentionDays))
	}
	// Delivered outbox rows go too. The outbox is a delivery mechanism, not an audit log — the
	// recommendations table already holds what the engine said — so keeping them forever would
	// grow a table nobody reads.
	if _, err := a.Storage.PruneDeliveredAlerts(maxAge); err != nil {
		a.Logger.Warn("failed to prune delivered alerts", zap.Error(err))
	}
}

// pollAlertsOnce evaluates the watchlist once and enqueues a notification only for symbols whose
// action changed since the last one actually announced. Every evaluation (HOLD included) is
// persisted for history, but only high-priority actions produce a notification — see
// isHighPriorityAction.
//
// The change-detection cursor is read from the database at the start of every poll, not held in
// memory across the process lifetime. Two reasons, both from the 2026-09-06 review:
//
//   - It used to be derived from the recommendations table, which GET /recommendations, the
//     recommendations page and post-transaction refreshes also write. That table records what the
//     engine said to anyone, which is not the same claim as what was announced to the user.
//   - A follower seeded its map once at startup. Promoted an hour later, it compared against a
//     baseline from before it became leader. Reading per poll costs one query over a few dozen
//     rows and removes the failure mode entirely.
//
// ctx and epoch are one snapshot of the leadership term (see Leader.Snapshot), taken before the
// batch and used throughout it: losing the lease mid-batch stops the batch rather than letting it
// finish on behalf of a lease somebody else now holds, and every write in the batch is fenced by
// the epoch the batch began under rather than by whatever the current one happens to be.
func (a *Application) pollAlertsOnce(ctx context.Context, epoch int64) {
	cursor, err := a.alertCursor()
	if err != nil {
		// Without a trustworthy cursor every symbol looks changed, which would re-announce the
		// whole watchlist. Skipping this poll is the cheaper mistake.
		a.Logger.Error("could not read the alert cursor; skipping this poll", zap.Error(err))
		a.AlertPoll.record(time.Now().UTC(), err)
		return
	}

	result, evalErr := a.Alerts.EvaluateAll(ctx)
	a.AlertPoll.record(time.Now().UTC(), evalErr)
	if evalErr != nil {
		a.Logger.Warn("alert evaluation completed with per-symbol errors", zap.Error(evalErr))
		a.Metrics.AlertPollErrors.Inc()
	}
	a.refreshWatchlistQuotes(ctx)

	for _, alert := range result {
		if ctx.Err() != nil {
			a.Logger.Info("stopped mid-poll: no longer the polling leader")
			return
		}
		a.persistRecommendation(alert.Recommendation)
		a.persistIndicatorSnapshot(alert.Recommendation.Symbol, alert.Bars)

		symbol := alert.Recommendation.Symbol
		action := alert.Recommendation.Action
		if cursor[symbol] == action {
			continue
		}
		if err := a.advanceCursor(ctx, alert, epoch); err != nil {
			if errors.Is(err, persistence.ErrNotLeader) {
				a.Logger.Info("stopped mid-poll: the lease changed hands")
				return
			}
			// Deliberately not treated as announced: an alert that could not be recorded has not
			// been announced, and marking it would silence the next poll too.
			a.Logger.Error("failed to record an alert transition",
				zap.String("symbol", symbol), zap.String("action", action), zap.Error(err))
			continue
		}
	}

	// Deliver in the same tick rather than waiting for the retry timer, so the common case (a
	// healthy channel) is still a notification sent seconds after the signal.
	a.deliverPendingAlerts(ctx, epoch)
}

// alertCursor reads the durable "last announced action per symbol". With no storage — tests, a
// degraded start — there is nothing durable to read and every action reads as new.
func (a *Application) alertCursor() (map[string]string, error) {
	if a.Storage == nil {
		return map[string]string{}, nil
	}
	return a.Storage.AlertCursor()
}

// advanceCursor records one transition. A notifiable action goes into the outbox and moves the
// cursor in the same transaction; anything else moves the cursor alone. Either way the cursor
// only advances if the durable record of *why* it advanced landed with it.
func (a *Application) advanceCursor(ctx context.Context, alert alerts.Alert, epoch int64) error {
	symbol := alert.Recommendation.Symbol
	action := alert.Recommendation.Action
	notifiable := a.Notifications.Configured() && isHighPriorityAction(action)

	if a.Storage == nil {
		if notifiable {
			_, err := a.Notifications.SendEmbed(ctx, buildAlertEmbed(alert), nil)
			return err
		}
		return nil
	}
	if !notifiable {
		return a.Storage.RecordSilentTransition(a.Leader.Identity(), epoch, symbol, action)
	}
	payload, err := persistence.MarshalAlertPayload(buildAlertEmbed(alert))
	if err != nil {
		return err
	}
	_, err = a.Storage.EnqueueAlert(a.Leader.Identity(), epoch,
		symbol, action, payload, a.Notifications.ChannelNames())
	return err
}

// deliverPendingAlerts works through the outbox, retrying each entry against only the channels
// that still owe it. Rows are claimed before sending, so two workers — a former leader still
// finishing a batch and its successor — cannot deliver the same entry concurrently.
//
// Delivery is at-least-once per channel: the send happens before its success is recorded, so a
// crash in between causes a retry that sends again. What is guaranteed is that nothing is
// silently dropped and that a retry does not fan back out to channels already recorded as
// delivered. See docs/deployment.md's "Delivery guarantee".
//
// Two things keep that guarantee from degrading into "older delivery state can overwrite newer".
// A batch stops sending the moment its claims lapse, rather than working through 50 entries on a
// lease that expired at the third. And every acknowledgement names the claim it belongs to, so
// one that arrives after the row has been taken over is refused (persistence.ErrClaimLost) rather
// than clearing the successor's claim or restoring channels the successor already delivered to.
func (a *Application) deliverPendingAlerts(ctx context.Context, epoch int64) {
	if a.Storage == nil || !a.Notifications.Configured() {
		return
	}
	entries, err := a.Storage.ClaimPendingAlerts(a.Leader.Identity(), maxOutboxBatch, outboxClaimTTL)
	if err != nil {
		a.Logger.Warn("could not claim pending alerts", zap.Error(err))
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			// The remaining claims expire on their own, so stopping here strands nothing —
			// whoever holds the lease next picks them up once the claim lapses.
			a.Logger.Info("stopped mid-delivery: no longer the polling leader")
			return
		}
		// The claim is a lease like any other, and sending on an expired one is how two workers
		// end up delivering the same alert while each believes it owns the row. Stopping is safe:
		// these rows are already claimable again, so the next tick — here or on another replica —
		// picks them up with a fresh claim.
		if !entry.ClaimExpiresAt.IsZero() && !time.Now().UTC().Before(entry.ClaimExpiresAt) {
			a.Logger.Warn("stopped mid-delivery: the outbox claims for this batch expired",
				zap.String("id", entry.ID), zap.Time("claim_expired_at", entry.ClaimExpiresAt))
			return
		}
		var embed notifications.Embed
		if err := json.Unmarshal(entry.Payload, &embed); err != nil {
			// Unreadable payload: nothing will ever make this deliverable, so retrying it every
			// 30 seconds forever would just be noise. Mark it done and say why.
			a.Logger.Error("dropping an undeliverable alert with an unreadable payload",
				zap.String("id", entry.ID), zap.String("symbol", entry.Symbol), zap.Error(err))
			a.recordDelivery(entry, nil, err)
			continue
		}
		targets := a.Notifications.ChannelsNamed(entry.PendingChannels)
		if len(targets) == 0 {
			// Every channel this alert was queued for has since been unconfigured. There is
			// nobody left to deliver to, so it is not pending any more.
			a.recordDelivery(entry, nil, nil)
			continue
		}
		failed, sendErr := a.Notifications.SendEmbed(ctx, embed, targets)
		stillPending := make([]string, 0, len(failed))
		for _, channel := range failed {
			stillPending = append(stillPending, channel.Name())
		}
		if err := a.recordDelivery(entry, stillPending, sendErr); err != nil {
			continue
		}
		if sendErr != nil {
			a.Logger.Error("alert delivery partially failed; will retry the failed channels",
				zap.String("symbol", entry.Symbol), zap.Strings("pending", stillPending), zap.Error(sendErr))
			continue
		}
		a.Metrics.AlertsSent.WithLabelValues(entry.Action).Inc()
		a.Logger.Info("alert notification sent",
			zap.String("symbol", entry.Symbol), zap.String("action", entry.Action))
	}
}

// recordDelivery acknowledges one entry under the claim it was handed out with, and reports
// whether the acknowledgement landed.
//
// A lost claim is logged at info, not as an error: it means this worker was superseded mid-send,
// which the design allows for. The consequence is a duplicate delivery by the successor, which is
// what at-least-once already promises — not corruption, which is what applying the write anyway
// would have caused.
func (a *Application) recordDelivery(entry persistence.OutboxEntry, stillPending []string, sendErr error) error {
	err := a.Storage.RecordAlertDelivery(entry.ID, entry.ClaimSeq, stillPending, sendErr)
	switch {
	case errors.Is(err, persistence.ErrClaimLost):
		a.Logger.Info("discarded a delivery result for an alert claimed by another worker",
			zap.String("id", entry.ID), zap.String("symbol", entry.Symbol))
	case err != nil:
		a.Logger.Warn("could not record an alert delivery", zap.String("id", entry.ID), zap.Error(err))
	}
	return err
}

// refreshWatchlistQuotes fetches and persists market.Quote (current live price + prior close)
// for every current watchlist symbol AND every symbol with an open portfolio position — called
// once per poll cycle (see pollAlertsOnce), independent of alert evaluation. Two consumers read
// this, on different pages: the Watchlist/Alerts "Today" column (see alertsPageHandler), and
// the Portfolio/Dashboard pages' live P&L and risk figures (see livePricedPositions) — a held
// position needs a live quote even if it was bought without ever being added to the watchlist,
// or was later removed from it, so this covers the union of the two, not just the watchlist.
// Best-effort per symbol: one provider failure just logs and moves on, matching Evaluate's own
// per-symbol handling.
func (a *Application) refreshWatchlistQuotes(ctx context.Context) {
	if a.Collector == nil || a.Collector.Provider == nil || a.Storage == nil {
		return
	}
	symbols := make(map[string]struct{})
	if items, err := a.Watchlist.List(); err != nil {
		a.Logger.Warn("could not list watchlist for quote refresh", zap.Error(err))
	} else {
		for _, item := range items {
			symbols[item.Symbol] = struct{}{}
		}
	}
	if a.Portfolio != nil {
		if positions, err := a.Portfolio.GetAllPositions(); err != nil {
			a.Logger.Warn("could not list positions for quote refresh", zap.Error(err))
		} else {
			for _, position := range positions {
				symbols[position.Symbol] = struct{}{}
			}
		}
	}

	for symbol := range symbols {
		if ctx.Err() != nil {
			// Same rule as the evaluation loop: a demoted leader stops refreshing quotes rather
			// than working through the rest of the list on a lease it no longer holds.
			a.Logger.Info("stopped mid-refresh: no longer the polling leader")
			return
		}
		quote, err := a.Collector.Provider.FetchQuote(ctx, symbol)
		outcome := "success"
		if err != nil {
			outcome = "error"
		}
		a.Metrics.ProviderRequests.WithLabelValues(marketProviderLabel, outcome, "quote").Inc()
		if err != nil {
			a.Logger.Warn("failed to fetch quote", zap.String("symbol", symbol), zap.Error(err))
			continue
		}
		quote, err = a.quoteToPLN(quote)
		if err != nil {
			a.Logger.Warn("failed to convert quote to PLN", zap.String("symbol", symbol), zap.String("currency", quote.Currency), zap.Error(err))
			continue
		}
		if err := a.Storage.UpsertSymbolQuote(symbol, quote); err != nil {
			a.Logger.Warn("failed to persist quote", zap.String("symbol", symbol), zap.Error(err))
		}
	}
}

// quoteToPLN converts a Quote's Price/PreviousClose from its native trading currency into PLN
// (currency.Base) — the currency Position.CurrentPrice and everything downstream of it
// (P&L, risk) is always denominated in (see portfolio.applyTransaction). Without this, a live
// USD quote overlaid onto a PLN-denominated position produced nonsense figures: a ~$130 stock
// read as if it were 130 PLN, roughly a 4x understatement.
//
// An empty or unrecognized currency is treated as already-PLN rather than converted: Yahoo
// reports "PLN" for every WSE (.WA) ticker already, and Atlas only converts the three
// currencies it actually supports (currency.Valid) — anything else (a blank response, or an
// instrument type Atlas' portfolio recording was never meant to hold) is left as-is rather than
// guessing at a rate.
func (a *Application) quoteToPLN(quote market.Quote) (market.Quote, error) {
	code := currency.Code(quote.Currency)
	if code == currency.Base {
		return quote, nil
	}
	if !currency.Valid(code) || a.ExchangeRates == nil {
		return market.Quote{}, fmt.Errorf("cannot convert quote currency %q to PLN", quote.Currency)
	}
	price, err := a.ExchangeRates.ToBase(quote.Price, code)
	if err != nil {
		return market.Quote{}, err
	}
	previousClose, err := a.ExchangeRates.ToBase(quote.PreviousClose, code)
	if err != nil {
		return market.Quote{}, err
	}
	quote.Price, quote.PreviousClose = price, previousClose
	return quote, nil
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
	// A degraded recommendation is a weaker claim than a complete one and must not arrive looking
	// identical to it; the reasons below say which input was missing.
	if rec.Degraded {
		fields = append(fields, notifications.EmbedField{Name: "Inputs", Value: "Incomplete — see reasons", Inline: true})
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
