package app

import (
	portfolioUC "atlas/internal/application/portfolio"
	"context"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// portfolioCookieName holds which portfolio's data the dashboard currently shows — same
// long-lived-preference pattern as langCookieName, not a session credential.
const portfolioCookieName = "atlas_portfolio"
const portfolioCookieMaxAge = 365 * 24 * 60 * 60

type portfolioContextKey struct{}

// forPortfolio binds a request-owned view to the cookie (or first portfolio), and scopes the
// storage handle to the request's context so a client that disconnects mid-request cancels the
// database work it started rather than leaving it running against nobody.
// Nested rendering reuses it, including a form's explicit portfolio selection.
func (a *Application) forPortfolio(r *http.Request) (*Application, error) {
	if a.Portfolio == nil {
		return a.forRequest(r), nil
	}
	scoped := *a.forRequest(r)
	if uc, ok := r.Context().Value(portfolioContextKey{}).(*portfolioUC.UseCase); ok {
		scoped.Portfolio = uc
		return &scoped, nil
	}
	id := ""
	if cookie, err := r.Cookie(portfolioCookieName); err == nil {
		id = cookie.Value
	}
	// The scoped storage handle, not the shared one: this is what actually makes an HTTP
	// portfolio write cancellable when the client disconnects.
	uc, err := a.Portfolio.ForPortfolio(id, scoped.Storage)
	if err != nil {
		return nil, err
	}
	scoped.Portfolio = uc
	*r = *r.WithContext(context.WithValue(r.Context(), portfolioContextKey{}, uc))
	return &scoped, nil
}

// setActivePortfolioCookie points portfolioCookieName at id — shared by portfolioSelectHandler
// (the dashboard tabs) and portfolioPageHandler (choosing a portfolio right on the record-
// transaction form), so both ways of picking a portfolio leave the dashboard showing the same one
// afterward.
func setActivePortfolioCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name: portfolioCookieName, Value: id, Path: "/",
		MaxAge: portfolioCookieMaxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// portfolioSelectHandler is the dashboard's portfolio tabs — a plain link (GET, not a form
// submit), since switching which portfolio is showing is a display preference, not a data
// mutation, same reasoning as langSetHandler being the only other cookie-setting nav control.
func (a *Application) portfolioSelectHandler(w http.ResponseWriter, r *http.Request) {
	scoped, err := a.forPortfolio(r)
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	a = scoped
	id := r.URL.Query().Get("id")
	if a.Portfolio != nil && id != "" {
		if err := a.Portfolio.SwitchPortfolio(id); err != nil {
			a.Logger.Warn("failed to switch active portfolio", zap.String("id", id), zap.Error(err))
		} else {
			setActivePortfolioCookie(w, a.Portfolio.ActivePortfolio())
		}
	}
	redirect := r.Referer()
	if !strings.HasPrefix(redirect, "/") || strings.HasPrefix(redirect, "//") {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// portfolioCreateHandler adds a new portfolio (Account page's "manage portfolios" section) — see
// portfolio.UseCase.CreatePortfolio for the MaxPortfolios cap. Does not switch to it; the tabs
// pick it up on the next render either way.
func (a *Application) portfolioCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	// The request-scoped view, not the shared use case. Creating, renaming and removing a
	// portfolio are database writes like any other; they were the three that still ran on the
	// process-wide use case and so ignored the request context however carefully it was scoped.
	scoped, err := a.forPortfolio(r)
	if err != nil {
		a.renderAccountPage(w, r, err.Error(), "")
		return
	}
	a = scoped
	name := strings.TrimSpace(r.FormValue("name"))
	created, err := a.Portfolio.CreatePortfolio(name)
	if err != nil {
		a.renderAccountPage(w, r, err.Error(), "")
		return
	}
	a.flashRedirect(w, r, "/dashboard/account", a.tr(r, "msg.portfolio_created", created.Name))
}

func (a *Application) portfolioRenameHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	scoped, err := a.forPortfolio(r) // see portfolioCreateHandler
	if err != nil {
		a.renderAccountPage(w, r, err.Error(), "")
		return
	}
	a = scoped
	id := r.FormValue("id")
	name := strings.TrimSpace(r.FormValue("name"))
	if err := a.Portfolio.RenamePortfolio(id, name); err != nil {
		a.renderAccountPage(w, r, err.Error(), "")
		return
	}
	a.flashRedirect(w, r, "/dashboard/account", a.tr(r, "msg.portfolio_renamed", name))
}

// portfolioRemoveHandler deletes a portfolio and everything in it (see
// portfolio.UseCase.DeletePortfolio, which refuses on the last remaining one). Unlike
// portfolioDeleteHandler (which removes one symbol's position), this removes an entire portfolio.
func (a *Application) portfolioRemoveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	scoped, err := a.forPortfolio(r) // see portfolioCreateHandler
	if err != nil {
		a.renderAccountPage(w, r, err.Error(), "")
		return
	}
	a = scoped
	id := r.FormValue("id")
	if err := a.Portfolio.DeletePortfolio(id); err != nil {
		a.renderAccountPage(w, r, err.Error(), "")
		return
	}
	// The deleted portfolio may have been the one the cookie names; clearing it here is simpler
	// and just as correct as trying to detect that case — SwitchPortfolio/Reload already fall
	// back to the first remaining portfolio whenever the cookie is absent or stale.
	http.SetCookie(w, &http.Cookie{Name: portfolioCookieName, Value: "", Path: "/", MaxAge: -1})
	a.flashRedirect(w, r, "/dashboard/account", a.tr(r, "msg.portfolio_removed"))
}

// forRequest scopes the storage handle to this request's lifetime, and with it every use case
// that holds a repository reference of its own. Everything else on the Application is shared and
// immutable per request, so a shallow copy is all this needs.
//
// The point is cancellation, not timeouts: the ten-second query deadline already bounded a stuck
// query, but a client that gave up after two seconds still paid for the full ten, and a browser
// that reloads a slow page three times used to leave three copies of the same work running.
//
// Scoping Storage alone did not achieve that, because a use case constructed at startup keeps the
// repository it was built with. Whichever use case reaches the database has to be scoped too, or
// the handler's careful scoping applies to a handle it never uses — the gap the 2026-09-06
// verification found in the watchlist paths after the portfolio ones were fixed.
func (a *Application) forRequest(r *http.Request) *Application {
	if a.Storage == nil {
		return a
	}
	scoped := *a
	scoped.Storage = a.Storage.WithContext(r.Context())
	scoped.Watchlist = a.Watchlist.ForRequest(scoped.Storage)
	return &scoped
}
