package app

import (
	portfolioUC "atlas/internal/application/portfolio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"atlas/internal/domain/portfolio"

	"go.uber.org/zap"
)

// refreshRecommendationTimeout bounds the background recommendation refresh below. Generous
// rather than tuned: it exists so a wedged provider or database call cannot pin a goroutine
// forever, not to make the refresh fast.
const refreshRecommendationTimeout = 60 * time.Second

// JSON API for positions and transactions. Every handler here is request-scoped to one portfolio
// (see forPortfolio) rather than reading a process-wide "active portfolio" — the bug the
// 2026-09-05 review opened with. Split out of handlers.go by resource.

// portfolioHandler returns the current position snapshot and risk context (GET) or records a
// transaction (POST).
//
// @Summary Get portfolio positions and risk, record a transaction, or remove a position
// @Description GET returns every current position plus the portfolio-wide risk snapshot.
// @Description POST records a BUY/SELL transaction (JSON body: portfolio.Transaction — symbol,
// @Description type, quantity, price, currency are required; PLN needs no currency conversion
// @Description setup) and updates the position; a transaction that sells a position down to zero
// @Description removes it from the position list, keeping the transaction in history.
// @Description DELETE (?symbol=) clears a symbol's current position aggregate without touching
// @Description transaction history — distinct from selling it via a POST.
// @Tags Portfolio
// @Accept json
// @Produce json
// @Param symbol query string false "Ticker to remove (DELETE only)"
// @Param transaction body portfolio.Transaction false "Transaction to record (POST only)"
// @Success 200 {object} map[string]any "GET: {"positions": [...], "risk": {...}}; DELETE: {"status": "removed"}"
// @Success 201 {object} map[string]string "POST: {"status": "recorded"}"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /portfolio [get]
// @Router /portfolio [post]
// @Router /portfolio [delete]
func (a *Application) portfolioHandler(w http.ResponseWriter, r *http.Request) {
	scoped, err := a.forPortfolio(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a = scoped
	switch r.Method {
	case http.MethodGet:
		positions, err := a.Portfolio.GetPositions()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		positions, _ = a.livePricedPositions(positions, nil)
		risk := portfolioUC.ComputeRiskSnapshot(positions)
		writeJSON(w, http.StatusOK, map[string]any{"positions": positions, "risk": risk})
	case http.MethodPost:
		var tx portfolio.Transaction
		if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.Portfolio.RecordTransaction(tx); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		go a.refreshRecommendation(tx.Symbol)
		writeJSON(w, http.StatusCreated, map[string]string{"status": "recorded"})
	case http.MethodDelete:
		symbol := r.URL.Query().Get("symbol")
		if symbol == "" {
			writeError(w, http.StatusBadRequest, errors.New("symbol query parameter is required"))
			return
		}
		if err := a.Portfolio.RemovePosition(symbol); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		go a.refreshRecommendation(symbol)
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	default:
		methodNotAllowed(w)
	}
}

// transactionsHandler lists every recorded transaction (GET) or removes one by id (DELETE),
// recomputing the affected symbol's position from what remains (see
// portfolio.UseCase.RemoveTransaction). Distinct from portfolioHandler's DELETE, which clears a
// symbol's current position aggregate without touching transaction history.
//
// @Summary List transaction history, or delete one and recompute its position
// @Description GET returns every recorded transaction. DELETE (?id=) removes one and replays the
// @Description affected symbol's remaining transactions to recompute its position from scratch.
// @Tags Portfolio
// @Produce json
// @Param id query string false "Transaction id to remove (DELETE only)"
// @Success 200 {object} map[string]any "GET: {"transactions": [...]}; DELETE: {"status": "removed"}"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /transactions [get]
// @Router /transactions [delete]
func (a *Application) transactionsHandler(w http.ResponseWriter, r *http.Request) {
	scoped, err := a.forPortfolio(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a = scoped
	switch r.Method {
	case http.MethodGet:
		transactions, err := a.Portfolio.GetTransactions()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"transactions": transactions})
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, errors.New("id query parameter is required"))
			return
		}
		symbol := a.transactionSymbol(id)
		if err := a.Portfolio.RemoveTransaction(id); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if symbol != "" {
			go a.refreshRecommendation(symbol)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	default:
		methodNotAllowed(w)
	}
}

// transactionSymbol looks up id's symbol before RemoveTransaction deletes it — needed so the
// caller can still call refreshRecommendation with the right symbol after removal. Returns "" if
// not found (already gone, or the id never existed), the same no-op stance RemoveTransaction
// itself takes for an unknown id.
func (a *Application) transactionSymbol(id string) string {
	transactions, err := a.Portfolio.GetTransactions()
	if err != nil {
		return ""
	}
	for _, tx := range transactions {
		if tx.ID == id {
			return tx.Symbol
		}
	}
	return ""
}

// refreshRecommendation recomputes and persists symbol's recommendation right after a
// transaction changes whether the portfolio holds it — opening a first position or selling one
// down to zero flips BuildRecommendation's HasPosition gate (BUY vs ACCUMULATE, SELL vs REDUCE).
// Without this, the previously-computed recommendation stays cached and visibly wrong (e.g.
// "REDUCE" showing for a symbol with no position — a real bug report) until the next alert-poll
// cycle recomputes it, up to ALERT_POLL_INTERVAL_SECONDS later. Runs in its own goroutine (called
// as "go a.refreshRecommendation(symbol)") since collectBars can be a real network call to the
// market provider — recording a transaction shouldn't wait on that. Best-effort: a failure here
// just logs, matching persistRecommendation's own stance — the transaction itself already
// succeeded and must not be affected by a stale-cache repair failing.
func (a *Application) refreshRecommendation(symbol string) {
	if a.Orchestrator == nil || a.Collector == nil {
		return
	}
	// Its own bounded context, not the request's: this deliberately outlives the transaction that
	// triggered it (see the comment above), so the request's context would cancel it the moment
	// the response was written. The bound exists so a wedged provider call cannot leave this
	// goroutine running for the life of the process.
	ctx, cancel := context.WithTimeout(context.Background(), refreshRecommendationTimeout)
	defer cancel()
	bars, err := a.collectBars(ctx, symbol, a.Config.CollectorWindow)
	if err != nil {
		a.Logger.Warn("failed to refresh recommendation after transaction", zap.String("symbol", symbol), zap.Error(err))
		return
	}
	rec, err := a.Orchestrator.BuildRecommendationFromBars(symbol, bars)
	if err != nil {
		a.Logger.Warn("failed to build recommendation after transaction", zap.String("symbol", symbol), zap.Error(err))
		return
	}
	a.Metrics.RecommendationsTotal.WithLabelValues(rec.Action).Inc()
	a.persistRecommendation(rec)
	a.persistIndicatorSnapshot(symbol, bars)
}
