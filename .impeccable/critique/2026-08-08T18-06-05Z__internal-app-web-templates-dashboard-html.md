---
target: internal/app/web/templates/dashboard.html
total_score: 16
max_score: 40
na_heuristics: 
p0_count: 2
p1_count: 3
timestamp: 2026-08-08T18-06-05Z
slug: internal-app-web-templates-dashboard-html
---
## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 1 | No "prices as of" timestamp, no poll-loop/health status, no NBP rate date; nav has no active/current-page indicator anywhere |
| 2 | Match System / Real World | 2 | "Volatility" is rendered as a PLN currency value (mean per-share deviation from cost), not a real volatility measure; "Drawdown" is actually unrealized-loss/exposure |
| 3 | User Control and Freedom | 3 | Read-only, reversible theme/language; no sort, no FX-line toggle |
| 4 | Consistency and Standards | 2 | Dashboard uses bare headings while sibling pages use cards; "Volatility" means PLN here, % on backtests; Log-out button styled identically to destructive Remove buttons |
| 5 | Error Prevention | 2 | Stale/zero CurrentPrice silently produces wrong P&L/Exposure with no staleness flag |
| 6 | Recognition Rather Than Recall | 1 | Symbols are plain text, not links to their recommendation; no active nav; no inline term definitions |
| 7 | Flexibility and Efficiency | 1 | No sort/filter/keyboard shortcuts/deep links; the app's own bulk-select pattern (used on Portfolio/Watchlist) is absent here |
| 8 | Aesthetic and Minimalist Design | 2 | ~11 stacked numbers per position row from unconditional FX conversion; zeroed risk table renders under the empty-state message |
| 9 | Error Recovery | 1 | Storage errors dump raw Go error text with no nav; catch-all "/" returns HTTP 200 for any unmatched path instead of 404 |
| 10 | Help and Documentation | 1 | No tooltip, no term definitions, no docs link anywhere on the page |
| **Total** | | **16/40** | **Poor — the page's job, not its pixels, needs the biggest fix** |

Both heuristics 7 and 10 were scored (not n/a): this is a daily-use Operate surface for one expert user, so accelerators and inline definitions are squarely applicable per the mode-applicability rule.

## Design Specificity Verdict

**LLM assessment**: This is a category-interchangeable page. Strip the column headers and it's a generic CRUD inventory view — nothing in composition, hierarchy, or interaction says "explainable ML decision support for one investor who never trades." PRODUCT.md's central principle is explainability-over-black-box, yet the page the user opens daily contains zero recommendations, zero confidence scores, zero model provenance. That candor is honored elsewhere in the codebase (`/dashboard/recommendations` shows "none loaded — stub") but not on `/`. The two genuinely product-specific artifacts — the multi-currency `money` helper and the never-trades disclaimer — are both under-leveraged: FX conversion is applied as indiscriminate noise on every cell rather than a feature, and the disclaimer is 12px gray small-print at the page's bottom rather than the confident statement PRODUCT.md frames it as.

**Deterministic scan**: `detect.mjs --json` against `dashboard.html` and `partials.html` returned exit code 0 with zero findings (empty JSON array). No false positives to report — there was nothing to flag. This is a real, useful data point on its own: it shows the bundled rule-based detector's ceiling. It catches markup-pattern slop (gradient text, generic bento grids, AI-tell patterns) but has no way to see that "Volatility" is mislabeled currency, that P&L has no color encoding, or that the page structurally can't answer the question the user opens it to ask. The manual review is where all of this run's real findings came from.

**Visual overlays**: Not available this run — no browser automation tool (Playwright/Puppeteer/screenshot) is exposed in this session, so the injection/overlay flow could not be attempted. A curl-based DOM sanity check confirmed the page renders correctly (200 OK, expected structure matching the templates, no server errors) but that is text/DOM confirmation only, not visual evidence.

## Overall Impression

The page is technically clean (zero detector findings, renders without error, real degradation handling for missing FX rates) but structurally empty relative to what the product promises. The single biggest opportunity: turn the page from a bare positions ledger into an actual "state of the world" view — starting with the cheapest, purely visual fixes (P&L color encoding, a totals/summary strip, page hierarchy) before touching what data it shows.

## What's Working

1. **The theme system** (`partials.html`) is better than most production dashboards: the inline script runs in `<head>` before first paint (no flash of wrong theme), `auto` tracks `prefers-color-scheme` live via a `matchMedia` listener, and an explicit user choice persists to `localStorage` and correctly suppresses auto-tracking.
2. **The empty state is authored, not defaulted**: "No open positions yet — see Portfolio to record a transaction" names the state, explains it's normal, and links the exact resolving page, fully translated.
3. **The FX degradation policy is correct**: when NBP is unreachable, the page renders PLN alone instead of erroring — the right call, only its silence is the problem (see Priority Issues).

## Priority Issues

**[P0] The dashboard omits the product.** The page renders only a positions table and a three-cell risk table — no recommendation, action, confidence, or alert count anywhere, though that's one nav click plus a dropdown away. *(Structural/content — noted for awareness; the fix below is scoped to what's actionable visually: making the existing Positions/Risk data easier to read is this run's focus per your "visual only" scope. Revisit the "Today" summary block idea separately if you want it later.)* Suggested command: `/impeccable shape` (deferred).

**[P0] Two of three "Portfolio Risk" numbers are mislabeled and dressed as currency.** `Volatility` is actually mean per-share deviation from cost, rendered as a PLN amount; `Drawdown` is actually unrealized-loss/exposure. *(This is a copy/data-semantics issue, not a pure visual one — flagging it, but out of this run's visual-only scope unless you want it included.)* Suggested command: `/impeccable clarify` (deferred).

**[P1] Nothing on the page is timestamped; degradation is silent.** No "prices as of" time, no NBP rate date, no warning when FX data is dropped. Suggested command: `/impeccable harden` (deferred — behavioral, not visual).

**[P1] P&L has no visual encoding and no total** — **this one is squarely visual and in scope.** The P&L cell is undifferentiated body text (gain and loss look identical apart from a minus sign); numeric columns aren't right-aligned or tabular; Quantity always renders 4 decimal places; no thousands separators; decimal separator is hardcoded `.` regardless of language; no totals row. The codebase already has the exact pattern needed (`actionClass` in `render.go` returns `text-success`/`text-danger` and is used elsewhere) — it's just never applied to money. Suggested command: `/impeccable layout` + `/impeccable colorize`.

**[P1] Accessibility/consistency defects in the shared visual layer — in scope.** `<html lang="en">` hardcoded while body content switches to Polish; no `active`/`aria-current` on any nav link ever; no skip link; no `scope="col"` on table headers; dashboard uses bare headings while sibling pages wrap peers in cards (visual inconsistency across the app); Log-out button (`btn-outline-danger`) is visually identical to genuinely destructive Remove buttons. Suggested command: `/impeccable audit` + `/impeccable layout`.

## Persona Red Flags

**Alex (Power User)**: No sort on any table column — finding the biggest loser among ten positions means reading every striped row with zero visual encoding on P&L. The Theme button always reads "Theme" with no active-state marker, while the Language dropdown four pixels away does mark its active item — an inconsistency inside one nav cluster that erodes trust in the UI's signals.

**Sam (Accessibility-Dependent User)**: Switches to Polish and the screen reader keeps its English voice (`lang="en"` hardcoded). Tabs through 10 nav controls with no announcement of current location — the brand link and "Dashboard" nav item both point at `/` and neither is ever marked current. Each position row announces ~11 numbers because of unconditional FX stacking with nothing marking the conversion as secondary.

**The Author (project-specific, from PRODUCT.md)**: Accounts in PLN and gets EUR *and* USD forced onto every money figure regardless of whether the portfolio needs it — the feature has become the page's dominant visual noise. Sees "Volatility: 1234.00 PLN" and immediately distrusts a number that should never be denominated in currency — on the one page meant to earn that trust.

## Minor Observations

- The Risk table renders `0.00 PLN / 0.00% / 0.00 PLN` directly beneath the "no positions" empty-state message — should be suppressed by the same conditional.
- `<h1>Dashboard</h1>` restates the tab title and the nav label while occupying the page's most prominent slot, informing nobody.
- Dashboard's Positions table is a strict subset of Portfolio's (same columns minus checkboxes/Remove) — visually it reads as a less-finished version of a page that already exists.
- On a 375px viewport, five columns each carrying stacked three-line currency values will force horizontal scroll with no visible affordance indicating scrollability.
- `text-body-secondary small` for the FX line sits near Bootstrap's default contrast floor in light mode — worth measuring against WCAG AA rather than assuming.

## Questions to Consider

- If a right-aligned, color-coded, tabular-numeral treatment made the P&L column readable at a glance, would that alone change how you use this page day to day?
- The theme-switcher code is the most careful piece of craft on the page — what would the rest of the UI look like if it got that same level of attention?
