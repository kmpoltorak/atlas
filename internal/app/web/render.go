// Package web is the server-rendered dashboard (Phase 8 — see implementation-plan.md):
// html/template pages served directly by atlas-api, no separate frontend build/deploy, no JS
// framework. Deliberately minimal, functional HTML — this is a personal decision-support tool,
// not a product with a design system.
package web

import (
	"bytes"
	"embed"
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
	"pnl":  func(p portfolio.Position) float64 { return (p.CurrentPrice - p.AverageCost) * p.Quantity },
	"join": func(items []string) string { return strings.Join(items, "; ") },
	// actionClass maps a recommendation action to Bootstrap's own text-color utility classes
	// (which carry !important) rather than a custom CSS rule — Bootstrap's table component sets
	// cell color via a higher-specificity selector (.table > :not(caption) > * > *) than a
	// plain custom class can beat without !important of its own, so fighting that with more
	// custom CSS is the wrong tool here.
	"actionClass": actionClass,
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
