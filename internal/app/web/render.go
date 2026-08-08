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
	"join": func(items []string) string { return strings.Join(items, "; ") },
	// actionClass maps a recommendation action to Bootstrap's own text-color utility classes
	// (which carry !important) rather than a custom CSS rule — Bootstrap's table component sets
	// cell color via a higher-specificity selector (.table > :not(caption) > * > *) than a
	// plain custom class can beat without !important of its own, so fighting that with more
	// custom CSS is the wrong tool here.
	"actionClass": actionClass,
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
