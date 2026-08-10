// Package web is the server-rendered dashboard (Phase 8 — see implementation-plan.md):
// html/template pages served directly by atlas-api, no separate frontend build/deploy, no JS
// framework. Deliberately minimal, functional HTML — this is a personal decision-support tool,
// not a product with a design system.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata" // embeds the IANA zoneinfo DB so localTime works even on a minimal image with no system tzdata installed

	"atlas/internal/domain/market"
	"atlas/internal/domain/portfolio"
)

//go:embed templates/*.html
var templatesFS embed.FS

// StaticFS serves Bootstrap (CSS/JS), bundled locally rather than loaded from a CDN — offline-
// friendly (no runtime dependency on jsdelivr/unpkg reachability), consistent with "no separate
// frontend build": these are plain vendored files, not an npm/webpack pipeline. See
// http_handler.go's "/static/" route.
//
//go:embed static
var StaticFS embed.FS

var funcMap = template.FuncMap{
	// percent turns a 0..1 ratio into a 0..100 number for "%.2f%%"-style formatting —
	// html/template has no arithmetic operators, so this has to happen in Go.
	"percent": func(x float64) float64 { return x * 100 },
	// pnl computes a position's unrealized profit/loss for the dashboard/portfolio tables.
	"pnl": func(p portfolio.Position) float64 { return (p.CurrentPrice - p.AverageCost) * p.Quantity },
	// totalPnL sums pnl across every position — the one number missing from both the dashboard
	// and portfolio pages before this: an investor otherwise has to add up the P&L column by
	// eye. Not the same figure as Risk.Exposure (total market value) or Risk.Drawdown
	// (unrealized loss only, losing positions) — this is the real net P&L, gains included.
	"totalPnL": func(positions []portfolio.Position) float64 {
		var total float64
		for _, p := range positions {
			total += (p.CurrentPrice - p.AverageCost) * p.Quantity
		}
		return total
	},
	// joinReasons translates each decision-engine rationale string (see reasonLabel) before
	// joining them — the raw values are hardcoded English produced by
	// internal/application/decision.UseCase, not user input, so they need the same T-style
	// lookup actionLabel gets, just applied per-item before the join.
	"joinReasons": func(lang Lang, reasons []string) string {
		translated := make([]string, len(reasons))
		for i, r := range reasons {
			translated[i] = reasonLabel(lang, r)
		}
		return strings.Join(translated, "; ")
	},
	"reasonLabel": reasonLabel,
	// yahooURL puts every symbol in the UI one click from its chart and fundamentals instead of
	// something to retype into a browser. Shared with the Discord alert embeds via the domain so
	// the two can't drift — see market.QuoteURL for why Yahoo specifically.
	"yahooURL": market.QuoteURL,
	// localTime converts a persisted-as-UTC timestamp (RFC3339 string or time.Time, depending on
	// the source field) to Atlas' display timezone — see atlasLocation. Atlas is single-user and
	// the Author is Poland-based, so this is a fixed zone, not something derived per-request.
	"localTime": localTime,
	// actionClass maps a recommendation action to Bootstrap's own text-color utility classes
	// (which carry !important) rather than a custom CSS rule — Bootstrap's table component sets
	// cell color via a higher-specificity selector (.table > :not(caption) > * > *) than a
	// plain custom class can beat without !important of its own, so fighting that with more
	// custom CSS is the wrong tool here.
	"actionClass": actionClass,
	// actionBadgeClass mirrors actionClass but for Bootstrap's badge component (the dashboard's
	// Active Alerts summary), which needs the "text-bg-*" background+text variant rather than
	// the plain "text-*" utility actionClass uses for table cell text.
	"actionBadgeClass": actionBadgeClass,
	// pnlClass mirrors actionClass's pattern (Bootstrap text-color utilities, not custom CSS,
	// for the same table-specificity reason) but keyed on a P&L value's sign instead of an
	// action string — gain and loss otherwise render as identical body text.
	"pnlClass": pnlClass,
	// T and actionLabel are the i18n lookups (see i18n.go) — every page passes its own .Lang
	// through so these can be called as {{T .Lang "key"}} / {{actionLabel .Lang .Action}}.
	"T":           T,
	"actionLabel": actionLabel,
	// safeHTML marks a translation value as pre-trusted HTML (a static <a>/<code> tag baked into
	// translations.go, never user input) so html/template doesn't escape it — see i18n.go's
	// translations doc comment for the safety contract this depends on.
	"safeHTML": func(s string) template.HTML { return template.HTML(s) }, //nolint:gosec // trusted, hardcoded translation strings only
	// money renders a currency.Base (PLN) amount as its primary figure plus a small secondary
	// line converting it to EUR/USD at today's live rate (rates, from Application.ExchangeRates.
	// Rates(), keyed "EUR"/"USD") — see internal/domain/currency. Only the numeric inputs are
	// ever interpolated (never user text), so building the HTML directly is safe. Missing/zero
	// rates (NBP unreachable and nothing cached yet) degrade to showing PLN alone, not an error —
	// a page must still render when NBP is down.
	"money": money,
}

func money(pln float64, rates map[string]float64) template.HTML {
	primary := fmt.Sprintf("%.2f PLN", pln)
	var secondary []string
	for _, code := range []string{"EUR", "USD"} {
		if rate := rates[code]; rate > 0 {
			secondary = append(secondary, fmt.Sprintf("%.2f %s", pln/rate, code))
		}
	}
	if len(secondary) == 0 {
		return template.HTML(primary) //nolint:gosec // numeric-only interpolation, no user input
	}
	return template.HTML(fmt.Sprintf(`%s<br><small class="text-body-secondary">&asymp; %s</small>`,
		primary, strings.Join(secondary, " / "))) //nolint:gosec // numeric-only interpolation, no user input
}

// atlasLocation is Atlas' fixed display timezone. Every persisted timestamp (transactions,
// alert triggers, backtest trades) is stored in UTC — see internal/application/alerts.UseCase
// and internal/app/web_handlers.go's time.Now().UTC() calls — so it has to be converted for
// display somewhere; a single-user, Poland-based tool doesn't need per-request/browser timezone
// detection, just one fixed zone.
var atlasLocation = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// localTime converts v — either a time.Time (backtesting.Trade.Timestamp) or an RFC3339 string
// (portfolio.Transaction.Timestamp, alerts.Alert.TriggeredAt, watchlist.Item.AddedAt — all
// persisted as plain strings, not time.Time) — to atlasLocation. An unparseable string returns
// the zero time rather than panicking a template render.
func localTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t.In(atlasLocation)
	case string:
		parsed, err := time.Parse(time.RFC3339, t)
		if err != nil {
			return time.Time{}
		}
		return parsed.In(atlasLocation)
	default:
		return time.Time{}
	}
}

func pnlClass(pln float64) string {
	switch {
	case pln > 0:
		return "text-success fw-semibold"
	case pln < 0:
		return "text-danger fw-semibold"
	default:
		return "text-body-secondary"
	}
}

func actionBadgeClass(action string) string {
	switch action {
	case "BUY", "ACCUMULATE":
		return "text-bg-success"
	case "SELL", "REDUCE":
		return "text-bg-danger"
	default: // HOLD, WAIT
		return "text-bg-secondary"
	}
}

func actionClass(action string) string {
	switch action {
	case "BUY", "ACCUMULATE":
		return "text-success fw-semibold"
	case "SELL", "REDUCE":
		return "text-danger fw-semibold"
	default: // HOLD, WAIT
		return "text-body-secondary"
	}
}

// templates parses every embedded *.html file once at startup — each page defines a uniquely
// named top-level template (e.g. {{define "dashboard"}}) alongside shared partials
// ("nav"/"style" in partials.html), so ExecuteTemplate(name) renders exactly one page with its
// shared chrome, per Go's standard multi-page html/template pattern.
var templates = template.Must(template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.html"))

// Render executes the named page template into a buffer first (not directly into w) so a
// template execution error is caught and reported as a real 500 instead of a silently
// truncated, half-written page — html/template can't un-write bytes already flushed to the
// client.
func Render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template render error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// RenderError renders a styled HTML error page (templates/error.html) with the given status
// code, instead of net/http's plain-text http.Error default — every page handler in
// web_handlers.go uses this so a failure looks like the rest of Atlas (nav, theme, layout), not
// a bare unstyled browser error page. Falls back to the plain-text default if the error template
// itself somehow fails to render — still better than nothing, and this shouldn't happen in
// practice (ErrorPage has no fields that can make execution fail).
func RenderError(w http.ResponseWriter, lang Lang, status int, message string) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, "error", ErrorPage{Lang: lang, Status: status, Message: message}); err != nil {
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
