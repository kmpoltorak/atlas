package app

import (
	"context"
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
	exchangerateUC "atlas/internal/application/exchangerate"
	notificationsUC "atlas/internal/application/notifications"
	portfolioUC "atlas/internal/application/portfolio"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/config"
	"atlas/internal/domain/alerts"
	"atlas/internal/domain/currency"
	"atlas/internal/domain/decision"
	"atlas/internal/domain/market"
	"atlas/internal/domain/portfolio"
	infraNotifications "atlas/internal/infrastructure/notifications"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

type loopStubProvider struct{}

func (loopStubProvider) FetchInstrumentInfo(_ context.Context, symbol string) (market.InstrumentInfo, error) {
	return market.InstrumentInfo{Symbol: symbol}, nil
}

func (loopStubProvider) FetchQuote(_ context.Context, symbol string) (market.Quote, error) {
	return market.Quote{Symbol: symbol}, nil
}

// A rising series long enough for the decision engine's momentum term to have a reading: it
// needs more than that term's lookback window, and the per-bar returns must vary, since a
// perfectly uniform climb has zero realized volatility to normalize against and reads as no
// signal. Volume is flat at the series average so volume confirmation stays at full weight.
func (loopStubProvider) FetchBars(_ context.Context, symbol string, window int) ([]market.Bar, error) {
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

	collector := &collectorUC.UseCase{Provider: loopStubProvider{}}
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())

	var notifiers []infraNotifications.Notifier
	if discordURL != "" {
		notifiers = append(notifiers, infraNotifications.NewDiscordClient(discordURL))
	}

	return &Application{
		Config:        config.Config{DiscordWebhookURL: discordURL},
		Metrics:       observability.NewMetrics(),
		Logger:        zap.NewNop(),
		Notifications: notificationsUC.NewUseCase(notifiers...),
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
	app.pollAlertsOnce(context.Background(), 0)

	if atomic.LoadInt32(&sent) != 1 {
		t.Fatalf("expected exactly one Discord notification, got %d", sent)
	}
}

func TestPollAlertsOnceSkipsWhenDiscordNotConfigured(t *testing.T) {
	app := newTestApplicationWithDiscord("")
	app.pollAlertsOnce(context.Background(), 0)
}

// Losing the lease mid-batch has to stop the batch, not merely prevent the next one. Before this,
// a replica that lost the lease kept writing results and sending notifications while its
// successor started doing the same.
func TestPollAlertsOnceStopsWhenTheLeadershipTermEnds(t *testing.T) {
	var sent int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sent, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := newTestApplicationWithDiscord(server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the lease is already gone by the time the batch starts its first symbol

	app.pollAlertsOnce(ctx, 0)

	if atomic.LoadInt32(&sent) != 0 {
		t.Fatalf("a batch running without leadership must send nothing, got %d", sent)
	}
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

	app.pollAlertsOnce(context.Background(), 0)

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

type fakeRateProvider struct{ rates map[currency.Code]float64 }

func (p fakeRateProvider) FetchRates() (map[currency.Code]float64, error) { return p.rates, nil }

type noopRateCache struct{}

func (noopRateCache) Get() (map[currency.Code]float64, bool) { return nil, false }
func (noopRateCache) Set(map[currency.Code]float64)          {}

// This is the exact bug a real portfolio hit live: a USD quote (e.g. SPCX at ~$11) overlaid
// straight onto a PLN-denominated position without conversion read as if it were 11 PLN, roughly
// a 4x understatement of P&L/risk.
func TestQuoteToPLNConvertsNonPLNCurrency(t *testing.T) {
	app := &Application{ExchangeRates: exchangerateUC.NewUseCase(
		fakeRateProvider{rates: map[currency.Code]float64{currency.USD: 3.8}}, noopRateCache{})}

	quote, err := app.quoteToPLN(market.Quote{Symbol: "SPCX", Price: 11, PreviousClose: 10, Currency: "USD"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if quote.Price != 41.8 || quote.PreviousClose != 38 {
		t.Fatalf("expected USD converted to PLN at 3.8, got %+v", quote)
	}
}

func TestQuoteToPLNLeavesPLNUnconverted(t *testing.T) {
	app := &Application{ExchangeRates: exchangerateUC.NewUseCase(
		fakeRateProvider{rates: map[currency.Code]float64{currency.USD: 3.8}}, noopRateCache{})}

	for _, code := range []string{"PLN"} {
		quote, err := app.quoteToPLN(market.Quote{Symbol: "ALE.WA", Price: 46.71, Currency: code})
		if err != nil {
			t.Fatalf("currency %q: unexpected error: %v", code, err)
		}
		if quote.Price != 46.71 {
			t.Fatalf("currency %q: expected the price left unconverted, got %v", code, quote.Price)
		}
	}
}
