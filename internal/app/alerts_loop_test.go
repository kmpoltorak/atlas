package app

import (
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
	"atlas/internal/domain/market"
	infraNotifications "atlas/internal/infrastructure/notifications"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

type loopStubProvider struct{}

func (loopStubProvider) FetchBars(symbol string, window int) ([]market.Bar, error) {
	base := time.Now()
	prices := []float64{100, 104, 109}
	bars := make([]market.Bar, 0, len(prices))
	for i, price := range prices {
		bars = append(bars, market.Bar{
			Symbol: symbol, Open: price - 4, High: price + 2, Low: price - 5, Close: price,
			Volume: 1200, Timestamp: base.Add(time.Duration(i) * time.Minute),
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
