package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"atlas/internal/domain/alerts"
	backtestingDomain "atlas/internal/domain/backtesting"
	"atlas/internal/domain/decision"
	"atlas/internal/domain/portfolio"
	"atlas/internal/domain/watchlist"
)

func TestRenderLoginPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "login", LoginPage{RequiresTOTP: true, Error: "invalid credentials"})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "invalid credentials") || !strings.Contains(body, `name="totp"`) {
		t.Fatalf("expected error message and TOTP field in rendered login page, got %s", body)
	}
}

func TestRenderDashboardPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "dashboard", DashboardPage{
		Positions: []portfolio.Position{{Symbol: "AAPL", Quantity: 10, AverageCost: 100, CurrentPrice: 120}},
		Risk:      portfolio.RiskSnapshot{Exposure: 1200, Drawdown: -0.1, Volatility: 15},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "AAPL") || !strings.Contains(body, "200.00") {
		t.Fatalf("expected position row with computed P&L (200.00) in rendered dashboard, got %s", body)
	}
}

func TestRenderPortfolioPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "portfolio", PortfolioPage{Success: "Transaction recorded"})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Transaction recorded") {
		t.Fatalf("expected success message in rendered portfolio page, got %s", rec.Body.String())
	}
}

func TestRenderRecommendationsPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "recommendations", RecommendationsPage{
		Symbol:  "AAPL",
		Symbols: []string{"AAPL", "MSFT"},
		Recommendation: &decision.Recommendation{
			Symbol: "AAPL", Action: "BUY", Confidence: 0.78, Reason: []string{"Momentum signal is favorable"},
			Score: 0.81, MarketCondition: "BULL",
		},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "BUY") || !strings.Contains(body, "BULL") || !strings.Contains(body, "Momentum signal is favorable") {
		t.Fatalf("expected action/market condition/reason in rendered recommendations page, got %s", body)
	}
	if !strings.Contains(body, `<option value="AAPL" selected>AAPL</option>`) || !strings.Contains(body, `<option value="MSFT" >MSFT</option>`) {
		t.Fatalf("expected a symbol dropdown with AAPL selected, got %s", body)
	}
	if !strings.Contains(body, `class="text-success fw-semibold"`) {
		t.Fatalf("expected BUY to use Bootstrap's text-success utility class (which carries "+
			"!important, needed to beat the table component's own higher-specificity color "+
			"rule), got %s", body)
	}
}

func TestActionClass(t *testing.T) {
	tests := map[string]string{
		"BUY": "text-success fw-semibold", "ACCUMULATE": "text-success fw-semibold",
		"SELL": "text-danger fw-semibold", "REDUCE": "text-danger fw-semibold",
		"HOLD": "text-body-secondary", "WAIT": "text-body-secondary",
	}
	for action, want := range tests {
		if got := actionClass(action); got != want {
			t.Fatalf("actionClass(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestRenderRecommendationsPageFallsBackToTextInputWithoutSymbols(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "recommendations", RecommendationsPage{Symbol: "AAPL"})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `name="symbol"`) || strings.Contains(rec.Body.String(), "<select") {
		t.Fatalf("expected a plain text input (no dropdown) when Symbols is empty, got %s", rec.Body.String())
	}
}

func TestRenderAlertsPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "alerts", AlertsPage{
		Alerts: []alerts.Alert{{
			Recommendation: decision.Recommendation{Symbol: "AAPL", Action: "REDUCE", Confidence: 0.65, Reason: []string{"Momentum signal is softening"}},
			TriggeredAt:    time.Now().Format(time.RFC3339),
		}},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "REDUCE") {
		t.Fatalf("expected REDUCE alert in rendered alerts page, got %s", rec.Body.String())
	}
}

func TestRenderBacktestsPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "backtests", BacktestsPage{
		Symbol: "AAPL", Window: 180, Capital: 10000,
		Result: &backtestingDomain.StrategyResult{
			Symbol: "AAPL", StrategyName: "atlas-orchestrator", StrategyVersion: "v1",
			TotalReturn: 0.12, SharpeRatio: 1.1, TradeCount: 1,
			Trades:       []backtestingDomain.Trade{{Timestamp: time.Now(), Action: "BUY", Price: 100, Quantity: 10, Confidence: 0.78}},
			MLEvaluation: &backtestingDomain.MLEvaluation{SampleSize: 40, Accuracy: 0.6, ROCAUC: 0.55},
		},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "atlas-orchestrator") || !strings.Contains(body, "12.00%") {
		t.Fatalf("expected strategy name and formatted return in rendered backtests page, got %s", body)
	}
}

func TestRenderWatchlistPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "watchlist", WatchlistPage{Items: []watchlist.Item{{Symbol: "AAPL", AddedAt: "2026-01-01T00:00:00Z"}}})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AAPL") {
		t.Fatalf("expected AAPL row in rendered watchlist page, got %s", rec.Body.String())
	}
}

func TestRenderAccountPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "account", AccountPage{Username: "admin", TOTPEnabled: true})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "admin") || !strings.Contains(body, "Enabled") || !strings.Contains(body, "Disable 2FA") {
		t.Fatalf("expected username, enabled status, and disable form in rendered account page, got %s", body)
	}
}

func TestRenderAccountPageWhenTOTPDisabled(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "account", AccountPage{Username: "admin", TOTPEnabled: false})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Set up 2FA") {
		t.Fatalf("expected a setup prompt when TOTP is disabled, got %s", rec.Body.String())
	}
}

func TestRenderTOTPEnrollPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "totp_enroll", TOTPEnrollPage{Secret: "ABCDEFGHIJ"})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ABCDEFGHIJ") || !strings.Contains(body, "/dashboard/account/totp/qr.png") {
		t.Fatalf("expected the manual secret and QR image src in rendered enrollment page, got %s", body)
	}
}

func TestRenderReportsTemplateErrorsAsHTTP500(t *testing.T) {
	rec := httptest.NewRecorder()
	// Missing template name entirely — ExecuteTemplate returns an error for an unknown name.
	Render(rec, "does-not-exist", nil)
	if rec.Code != 500 {
		t.Fatalf("expected 500 for an unknown template name, got %d", rec.Code)
	}
}
