package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	alertsUC "atlas/internal/application/alerts"
	analyzerUC "atlas/internal/application/analyzer"
	collectorUC "atlas/internal/application/collector"
	decisionUC "atlas/internal/application/decision"
	notificationsUC "atlas/internal/application/notifications"
	portfolioUC "atlas/internal/application/portfolio"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/config"
	"atlas/internal/domain/alerts"
	"atlas/internal/domain/decision"
	"atlas/internal/domain/market"
	"atlas/internal/domain/portfolio"
	infraNotifications "atlas/internal/infrastructure/notifications"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

type loopStubProvider struct{}

// A rising series long enough for the decision engine's momentum term to have a reading: it
// needs more than that term's lookback window, and the per-bar returns must vary, since a
// perfectly uniform climb has zero realized volatility to normalize against and reads as no
// signal. Volume is flat at the series average so volume confirmation stays at full weight.
func (loopStubProvider) FetchBars(symbol string, window int) ([]market.Bar, error) {
	base := time.Now().Add(-30 * time.Hour)
	bars := make([]market.Bar, 0, 30)
	price := 100.0
	for i := 0; i < 30; i++ {
		if i%2 == 0 {
			price *= 1.015
		} else {
			price *= 1.005
		}
		bars = append(bars, market.Bar{
			Symbol: symbol, Open: price / 1.004, High: price * 1.002, Low: price / 1.008,
			Close: price, Volume: 1200, Timestamp: base.Add(time.Duration(i) * time.Hour),
		})
	}
	return bars, nil
}

func newTestApplicationWithDiscord(discordURL string) *Application {
	watchlist := watchlistUC.NewUseCase()
	_ = watchlist.Add("AAPL", "2026-08-05T00:00:00Z")

	collector := collectorUC.NewUseCase(loopStubProvider{})
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())
	discord := infraNotifications.NewDiscordClient(discordURL)

	return &Application{
		Config:        config.Config{DiscordWebhookURL: discordURL},
		Metrics:       observability.NewMetrics(),
		Logger:        zap.NewNop(),
		Notifications: notificationsUC.NewUseCase(discord),
		Watchlist:     watchlist,
		Alerts:        alertsUC.NewUseCase(watchlist, collector, orchestrator, 30),
	}
}

func TestPollAlertsOnceSendsDiscordNotificationForNewAction(t *testing.T) {
	var sent int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sent, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newTestApplicationWithDiscord(server.URL)
	app.pollAlertsOnce(map[string]string{})

	if atomic.LoadInt32(&sent) != 1 {
		t.Fatalf("expected exactly one Discord notification, got %d", sent)
	}
}

func TestPollAlertsOnceSkipsRepeatedSameAction(t *testing.T) {
	var sent int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sent, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newTestApplicationWithDiscord(server.URL)
	lastAction := map[string]string{"AAPL": "BUY"}
	app.pollAlertsOnce(lastAction)

	if atomic.LoadInt32(&sent) != 0 {
		t.Fatalf("expected no notification for a repeated action, got %d", sent)
	}
}

func TestPollAlertsOnceSkipsWhenDiscordNotConfigured(t *testing.T) {
	app := newTestApplicationWithDiscord("")
	app.pollAlertsOnce(map[string]string{})
}

// ACCUMULATE is a partial buy against a position you already hold, and it notifies for the same
// reason REDUCE (its partial-sell counterpart) does — it asks the reader to do something. The
// two were split for a while, with REDUCE notifying and ACCUMULATE silent; this test pins the
// symmetry so that asymmetry can't quietly return.
func TestPollAlertsOnceNotifiesForAccumulate(t *testing.T) {
	var sent int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sent, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newTestApplicationWithDiscord(server.URL)
	// Same bullish bars as TestPollAlertsOnceSendsDiscordNotificationForNewAction (which
	// produce BUY with no position), but with an existing AAPL position this time — the
	// strong-bullish score becomes ACCUMULATE instead.
	app.Portfolio = portfolioUC.NewUseCase()
	app.Portfolio.Positions = []portfolio.Position{{Symbol: "AAPL", Quantity: 5, AverageCost: 90, CurrentPrice: 100}}
	app.Alerts = alertsUC.NewUseCase(app.Watchlist, app.Alerts.Collector,
		decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), app.Portfolio, decisionUC.NewUseCase()), 30)

	lastAction := map[string]string{}
	app.pollAlertsOnce(lastAction)

	if lastAction["AAPL"] != "ACCUMULATE" {
		t.Fatalf("expected ACCUMULATE to be tracked for change-detection, got %q", lastAction["AAPL"])
	}
	if atomic.LoadInt32(&sent) != 1 {
		t.Fatalf("expected exactly one Discord notification for ACCUMULATE, got %d", sent)
	}
}

func TestIsHighPriorityAction(t *testing.T) {
	// The four actionable calls notify; the two "no decision needed" ones don't. HOLD never
	// even reaches this function in practice (alerts.UseCase filters it out before the loop
	// sees it), but it is covered here so the intent stays explicit.
	for action, want := range map[string]bool{
		"BUY": true, "ACCUMULATE": true, "REDUCE": true, "SELL": true,
		"HOLD": false, "WAIT": false, "": false, "buy": false,
	} {
		if got := isHighPriorityAction(action); got != want {
			t.Errorf("isHighPriorityAction(%q) = %v, want %v", action, got, want)
		}
	}
}

// buildAlertEmbed is what a Discord alert actually looks like, so pin its shape: the colour that
// distinguishes an add from a trim at a glance, the linked title, the reasons as separate lines
// rather than a semicolon-joined run-on, and the numbers as an inline field row.
func TestBuildAlertEmbedRendersAReadableCard(t *testing.T) {
	embed := buildAlertEmbed(alerts.Alert{
		Recommendation: decision.Recommendation{
			Symbol: "ORCL", Action: "BUY", Confidence: 0.6686, Score: 0.8343,
			MarketCondition: "BULL",
			Reason:          []string{"Momentum signal is favorable", "ML signal supportive"},
		},
		TriggeredAt: "2026-08-10T16:05:00Z",
	})

	if embed.Title != "ORCL — BUY" {
		t.Errorf("title = %q", embed.Title)
	}
	if embed.URL != "https://finance.yahoo.com/quote/ORCL/" {
		t.Errorf("url = %q, want the symbol's quote page so an alert is one tap from the chart", embed.URL)
	}
	if embed.Color != actionColors["BUY"] {
		t.Errorf("color = %d, want BUY's %d", embed.Color, actionColors["BUY"])
	}
	if embed.Description != "• Momentum signal is favorable\n• ML signal supportive" {
		t.Errorf("description = %q, want one bullet per line", embed.Description)
	}
	if len(embed.Fields) != 3 {
		t.Fatalf("expected confidence, score and market fields, got %+v", embed.Fields)
	}
	if embed.Fields[0].Value != "67%" || embed.Fields[1].Value != "0.834" {
		t.Errorf("fields = %+v", embed.Fields)
	}
	if embed.Timestamp != "2026-08-10T16:05:00Z" {
		t.Errorf("timestamp = %q", embed.Timestamp)
	}

	rendered, _ := json.MarshalIndent(embed, "", "  ")
	t.Logf("embed payload:\n%s", rendered)
}

// Market condition is only populated when the classifier had enough history. An empty field
// renders as a blank column in Discord rather than being skipped, so it must not be added.
func TestBuildAlertEmbedOmitsUnknownMarketCondition(t *testing.T) {
	embed := buildAlertEmbed(alerts.Alert{
		Recommendation: decision.Recommendation{Symbol: "ORCL", Action: "SELL", Reason: []string{"Momentum signal is weak"}},
	})
	if len(embed.Fields) != 2 {
		t.Fatalf("expected market condition to be omitted when unknown, got %+v", embed.Fields)
	}
	if embed.Color != actionColors["SELL"] {
		t.Errorf("SELL must be visually distinct from BUY, got %d", embed.Color)
	}
}

// A failed Discord send must not be recorded as delivered. The loop used to mark the action seen
// before attempting the send, which turned a transient webhook failure into an alert the Author
// never saw and never would — every later poll considered it already handled.
func TestPollAlertsOnceRetriesAfterAFailedSend(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fail the first attempt, accept the second.
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newTestApplicationWithDiscord(server.URL)
	lastAction := map[string]string{}

	app.pollAlertsOnce(lastAction)
	if lastAction["AAPL"] != "" {
		t.Fatalf("a failed send must leave the action unrecorded so it retries, got %q", lastAction["AAPL"])
	}

	app.pollAlertsOnce(lastAction)
	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("expected the next poll to retry the failed alert, got %d attempts", attempts)
	}
	if lastAction["AAPL"] != "BUY" {
		t.Fatalf("a delivered alert must be recorded, got %q", lastAction["AAPL"])
	}

	// And once delivered, it stays quiet.
	app.pollAlertsOnce(lastAction)
	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("expected no further sends for an unchanged action, got %d attempts", attempts)
	}
}

// Actions that never reach Discord still have to be tracked, or a later change would be measured
// against a stale baseline and go unannounced.
func TestPollAlertsOnceTracksNonNotifyingActionsWithoutSending(t *testing.T) {
	app := newTestApplicationWithDiscord("")
	lastAction := map[string]string{}

	app.pollAlertsOnce(lastAction)

	if lastAction["AAPL"] != "BUY" {
		t.Fatalf("expected the action to be tracked even with no webhook configured, got %q", lastAction["AAPL"])
	}
}
