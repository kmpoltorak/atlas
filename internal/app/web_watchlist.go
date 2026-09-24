package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"atlas/internal/app/web"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/domain/watchlist"
	"atlas/internal/infrastructure/persistence"

	"go.uber.org/zap"
)

// Watchlist pages: the tracked-symbol table, its import/export, and the symbol-info refresh that
// fills in names and exchanges. Split out of web_handlers.go by feature.

// watchlistPageHandler lists watched symbols (GET) and adds one (POST) — the dashboard
// equivalent of GET/POST /watchlist.
func (a *Application) watchlistPageHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.renderWatchlistPage(w, r, "", "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			a.renderWatchlistPage(w, r, a.tr(r, "msg.invalid_form"), "")
			return
		}
		symbol := strings.ToUpper(strings.TrimSpace(r.FormValue("symbol")))
		if err := a.Watchlist.Add(symbol, time.Now().UTC().Format(time.RFC3339)); err != nil {
			a.renderWatchlistPage(w, r, err.Error(), "")
			return
		}
		if err := a.fetchAndSaveSymbolInfo(r.Context(), symbol); err != nil {
			a.Logger.Warn("failed to fetch symbol info for newly added watchlist item", zap.String("symbol", symbol), zap.Error(err))
		}
		a.renderWatchlistPage(w, r, "", a.tr(r, "msg.watchlist_added", symbol))
	default:
		methodNotAllowed(w)
	}
}

// fetchAndSaveSymbolInfo best-effort fetches and persists what a symbol actually is (see
// market.InstrumentInfo) — its display name and instrument type (EQUITY/ETF/FUTURE/...), read
// off the market provider and shown on the Watchlist page so a ticker doesn't have to be looked
// up elsewhere. Called right after a symbol is added (see watchlistPageHandler) and by
// watchlistRefreshInfoHandler for symbols added before this existed. Best-effort: the watchlist
// item itself is already saved by the time this runs, so a lookup failure here should leave that
// row's Name/Type blank, not fail the request — same non-fatal stance as persistIndicatorSnapshot.
func (a *Application) fetchAndSaveSymbolInfo(ctx context.Context, symbol string) error {
	if a.Collector == nil || a.Collector.Provider == nil || a.Storage == nil {
		return nil
	}
	info, err := a.Collector.Provider.FetchInstrumentInfo(ctx, symbol)
	if err != nil {
		return err
	}
	return a.Storage.UpsertSymbolInfo(symbol, info)
}

// watchlistRefreshInfoHandler backfills market.InstrumentInfo for every watchlist symbol that
// doesn't have one yet (see symbolsMissingInfo) — the one-time catch-up for symbols added
// before this feature existed; new adds already fetch it inline (watchlistPageHandler).
// Sequential and synchronous: this runs once per deliberate button click, not on every page
// render, so paying provider latency here — unlike the live-recompute-on-every-request bugs
// already fixed on this dashboard (2cd6683, and the Alerts page) — is the expected cost of the
// action the click asked for, not a regression of the same kind.
func (a *Application) watchlistRefreshInfoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	items, err := a.Watchlist.List()
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	existing, err := a.Storage.SymbolInfoByTicker()
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}

	missing := symbolsMissingInfo(items, existing)
	updated, failed := 0, 0
	for _, symbol := range missing {
		if err := a.fetchAndSaveSymbolInfo(r.Context(), symbol); err != nil {
			a.Logger.Warn("failed to fetch symbol info", zap.String("symbol", symbol), zap.Error(err))
			failed++
			continue
		}
		updated++
	}

	message := a.tr(r, "msg.info_fetched", updated)
	switch {
	case len(missing) == 0:
		message = a.tr(r, "msg.info_complete")
	case failed > 0:
		message += a.tr(r, "msg.info_failed_suffix", failed)
	}
	a.renderWatchlistPage(w, r, "", message)
}

// symbolsMissingInfo reports which watchlist symbols have no recorded market.InstrumentInfo yet
// — split out from watchlistRefreshInfoHandler so it's testable without a database behind it,
// same reasoning as countActionable/filterCurrentAlerts above.
func symbolsMissingInfo(items []watchlist.Item, existing map[string]persistence.SymbolInfo) []string {
	var missing []string
	for _, item := range items {
		if _, ok := existing[item.Symbol]; !ok {
			missing = append(missing, item.Symbol)
		}
	}
	return missing
}

// watchlistDeleteHandler removes a symbol — the dashboard equivalent of DELETE /watchlist (see
// watchlist.UseCase.Remove).
// watchlistDeleteHandler untracks watchlist symbols (see watchlist.UseCase.RemoveMany). Serves
// both the checkbox-select "Delete selected" action and the per-row Remove buttons — see
// formValues.
func (a *Application) watchlistDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderWatchlistPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	symbols := upperAll(formValues(r, "symbols", "symbol"))
	if len(symbols) == 0 {
		a.renderWatchlistPage(w, r, a.tr(r, "msg.no_symbols_selected"), "")
		return
	}
	if err := a.Watchlist.RemoveMany(symbols); err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	message := a.tr(r, "msg.watchlist_removed_many", len(symbols))
	if len(symbols) == 1 {
		message = a.tr(r, "msg.watchlist_removed_one", symbols[0])
	}
	a.renderWatchlistPage(w, r, "", message)
}

func (a *Application) renderWatchlistPage(w http.ResponseWriter, r *http.Request, errMsg, successMsg string) {
	items, err := a.Watchlist.List()
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	web.Render(w, "watchlist", web.WatchlistPage{
		Lang: a.resolveLang(r), Items: items, Info: a.loadSymbolInfo(), Error: errMsg, Success: successMsg,
	})
}

// loadSymbolInfo reads one symbols row per ticker (see fetchAndSaveSymbolInfo/
// refreshWatchlistQuotes) — Name/Type for the Watchlist page, LastPrice/PreviousClose for the
// Alerts page's "Today" column (see alertsPageHandler); each page's template only reads the
// fields it renders. One query serves both since it's the same underlying row. Best-effort like
// modelOptions above: a failed query costs those columns and nothing else, since neither page's
// core function depends on this.
func (a *Application) loadSymbolInfo() map[string]web.SymbolInfo {
	if a.Storage == nil {
		return nil
	}
	stored, err := a.Storage.SymbolInfoByTicker()
	if err != nil {
		a.Logger.Warn("failed to load symbol info", zap.Error(err))
		return nil
	}
	info := make(map[string]web.SymbolInfo, len(stored))
	for ticker, symbolInfo := range stored {
		info[ticker] = web.SymbolInfo{
			Name: symbolInfo.Name, Type: symbolInfo.AssetClass,
			LastPrice: symbolInfo.LastPrice, PreviousClose: symbolInfo.PreviousClose,
		}
	}
	return info
}

// watchlistImportPageHandler applies an uploaded watchlist export — the dashboard counterpart of
// POST /watchlist/import, and the path that actually matters for the feature's purpose: moving a
// real watchlist between environments without re-entering several dozen symbols by hand.
//
// The file is read through a bounded reader rather than trusting the multipart form's own limit,
// and the destructive mode has to arrive explicitly as "replace" — a missing or unrecognised
// value merges, so a mangled form can never silently clear the watchlist.
func (a *Application) watchlistImportPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	// MaxBytesReader before parsing, not ParseMultipartForm's own argument: that argument bounds
	// how much the parser keeps in memory, and everything past it spills to a temp file, so on its
	// own it capped nothing. The extra MiB is headroom for the multipart envelope (boundaries,
	// headers, the mode field) around a file that is allowed to be maxWatchlistImportBytes.
	r.Body = http.MaxBytesReader(w, r.Body, maxWatchlistImportBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		a.renderWatchlistPage(w, r, web.T(a.resolveLang(r), "watchlist.import_invalid"), "")
		return
	}
	defer r.MultipartForm.RemoveAll()
	mode, err := watchlistUC.ParseImportMode(r.FormValue("mode"))
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		a.renderWatchlistPage(w, r, web.T(a.resolveLang(r), "watchlist.import_no_file"), "")
		return
	}
	defer file.Close()

	// Read one byte past the limit so an oversized file is detected rather than silently
	// truncated: a truncated read can leave a valid JSON prefix, which would import a file whose
	// remainder was thrown away without anyone being told.
	body, err := io.ReadAll(io.LimitReader(file, maxWatchlistImportBytes+1))
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	if len(body) > maxWatchlistImportBytes {
		a.renderWatchlistPage(w, r, web.T(a.resolveLang(r), "watchlist.import_too_large"), "")
		return
	}
	items, err := decodeWatchlistImport(body)
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	result, err := a.Watchlist.Import(items, mode, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	a.renderWatchlistPage(w, r, "", fmt.Sprintf(
		web.T(a.resolveLang(r), "watchlist.import_done"), result.Added, result.Skipped, result.Removed))
}

// watchlistSymbols returns the watchlist as a plain []string, alphabetically sorted — used to
// populate the recommendations/backtests page dropdowns so the common case doesn't require
// typing a ticker by hand. Sorted here for the dropdown only; the Watchlist page's own table
// keeps a.Watchlist.List()'s natural (added) order.
func (a *Application) watchlistSymbols() ([]string, error) {
	items, err := a.Watchlist.List()
	if err != nil {
		return nil, err
	}
	symbols := make([]string, len(items))
	for i, item := range items {
		symbols[i] = item.Symbol
	}
	sort.Strings(symbols)
	return symbols, nil
}
