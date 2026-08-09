# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

A single primary user: the Author, a personal stock/ETF investor who owns and operates Atlas
for their own portfolio. This is confirmed durable, not a current-scope limitation — Atlas
stays strictly single-user permanently, with no plan to add other accounts or share access.
The Author checks the dashboard directly on a regular (daily/weekly) basis rather than relying
only on Discord push notifications as the primary signal.

## Product Purpose

Atlas is an AI-powered decision-support platform for a personal stock/ETF portfolio. For every
watched symbol it collects real market history, computes technical indicators, runs ML
predictions, and blends all of that with the user's actual portfolio state and portfolio-wide
risk into an explainable recommendation — BUY, ACCUMULATE, HOLD, REDUCE, SELL, or WAIT — each
with a confidence score and a human-readable reason. Success means the Author trusts the
recommendation enough to act on it themselves, specifically because the reasoning is visible,
not because a black-box score says so.

## Positioning

Atlas never places trades — no brokerage integration, no execution path, by permanent design.
It positions as a transparent analytical assistant, not a signal service or a trading bot. What
a generic stock-signal tool doesn't do, and Atlas does: blend a real trained ML model (not a
marketing black box — its holdout metrics and known data-quality caveats are surfaced, not
hidden) with the user's own actual position, cost basis, and portfolio-wide risk, and persist
every recommendation with full traceability of which inputs drove it.

## Operating Context

Self-hosted, single Go binary plus TimescaleDB and Redis, run via Docker Compose (or the
Kubernetes manifests under `deploy/`) — no managed/cloud SaaS dependency. A background loop
polls the watchlist on an interval and is the source of both live alerting and the price
history ML training later consumes. Two independent Discord channels carry notifications: one
for investment alerts (BUY/SELL/REDUCE), one for "is Atlas itself healthy" signals (infra
alerts, alert-poll failures, training run outcomes). Prometheus/Grafana provide observability.
Login is optional (single admin account, password + TOTP 2FA); when configured, repeated failed
attempts lock out further login attempts for a cooldown window. ML models can be retrained and
hot-swapped from the dashboard's Account page without a restart, via an optional internal-only
trainer sidecar.

## Capabilities and Constraints

- Real trained ML models (XGBoost, exported to ONNX, run natively in Go) predict correction and
  recovery probability; a deterministic RSI-derived stub is the fallback when no model is
  loaded — always clearly distinguished, never silently substituted.
- Multi-currency transaction recording (PLN/EUR/USD) with PLN as the internal accounting
  currency.
- No brokerage/trade-execution integration exists or is planned — this is a constraint, not a
  gap.
- Permanently single-user: no multi-user accounts, no OAuth2/OIDC. This was explicitly
  confirmed and is not an MVP shortcut.
- Server-rendered web UI only (no separate SPA/JS-framework frontend) — a deliberate permanent
  choice, not an unfinished phase.
- ML training data is real but currently thin and skewed by construction (recovery is only a
  defined question after a correction happens) — this honesty is itself a product constraint:
  future work must keep surfacing that caveat, not smooth it over.

## Brand Commitments

Product name is "Atlas" (tagline used in docs: "AI Portfolio Advisor"). No logo exists yet; the
browser tab favicon is a plain glyph (a white "A" on Bootstrap's own primary blue), not a
designed brand mark. No committed color palette or typography beyond the current stock
Bootstrap 5.3 defaults — this is open ground for future design work, not a locked identity. The one
consistent, deliberate voice trait across the existing docs and UI is candor: known limitations
(a deterministic stub standing in for a real model, thin ML training data, unfinished
scaffolding) are always labeled as such rather than glossed over. Preserve that candor in any
future copy or UI work.

## Evidence on Hand

A real, live system: real TimescaleDB-backed recommendations/portfolio/watchlist data, real
Prometheus/Grafana dashboards, real trained ONNX models with printed holdout AUC/accuracy. No
product screenshots, marketing assets, or logo currently exist. No customers, testimonials, or
case studies exist or are applicable — Atlas has exactly one user — future work must not
fabricate any of these.

## Product Principles

1. Never fabricate — no placeholder data, invented metrics, or invented testimonials; every
   number shown is either real or explicitly labeled as a stub/estimate.
2. Explainability over black-box confidence — every recommendation must show which signals
   drove it, not just a score.
3. Never execute trades — analytical assistant only, permanently, no exceptions.
4. Single-user by design, not by limitation — do not build multi-tenant complexity "just in
   case."
5. Honest about uncertainty — confidence scores and known data-quality caveats are surfaced,
   never hidden to look more finished than the system actually is.
