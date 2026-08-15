package app

import (
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// portfolioCookieName holds which portfolio's data the dashboard currently shows — same
// long-lived-preference pattern as langCookieName, not a session credential.
const portfolioCookieName = "atlas_portfolio"
const portfolioCookieMaxAge = 365 * 24 * 60 * 60

// syncActivePortfolio makes a.Portfolio.ActivePortfolioID match the request's portfolio cookie
// before any handler reads or writes Positions/Transactions — every page/JSON handler that
// touches a.Portfolio calls this first. A missing cookie, or one naming a portfolio that no
// longer exists, is not an error: SwitchPortfolio/Reload fall back to the first portfolio on
// their own (see portfolio.UseCase.Reload).
func (a *Application) syncActivePortfolio(r *http.Request) error {
	if a.Portfolio == nil {
		return nil
	}
	cookie, err := r.Cookie(portfolioCookieName)
	if err != nil || cookie.Value == a.Portfolio.ActivePortfolio() {
		return nil
	}
	return a.Portfolio.SwitchPortfolio(cookie.Value)
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
	id := r.URL.Query().Get("id")
	if a.Portfolio != nil && id != "" {
		if err := a.Portfolio.SwitchPortfolio(id); err != nil {
			a.Logger.Warn("failed to switch active portfolio", zap.String("id", id), zap.Error(err))
		} else {
			setActivePortfolioCookie(w, id)
		}
	}
	redirect := r.Referer()
	if redirect == "" {
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
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	created, err := a.Portfolio.CreatePortfolio(name)
	if err != nil {
		a.renderAccountPage(w, r, err.Error(), "")
		return
	}
	a.flashRedirect(w, r, "/dashboard/account", "Portfolio \""+created.Name+"\" created.")
}

func (a *Application) portfolioRenameHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	id := r.FormValue("id")
	name := strings.TrimSpace(r.FormValue("name"))
	if err := a.Portfolio.RenamePortfolio(id, name); err != nil {
		a.renderAccountPage(w, r, err.Error(), "")
		return
	}
	a.flashRedirect(w, r, "/dashboard/account", "Portfolio renamed to \""+name+"\".")
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
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	id := r.FormValue("id")
	if err := a.Portfolio.DeletePortfolio(id); err != nil {
		a.renderAccountPage(w, r, err.Error(), "")
		return
	}
	// The deleted portfolio may have been the one the cookie names; clearing it here is simpler
	// and just as correct as trying to detect that case — SwitchPortfolio/Reload already fall
	// back to the first remaining portfolio whenever the cookie is absent or stale.
	http.SetCookie(w, &http.Cookie{Name: portfolioCookieName, Value: "", Path: "/", MaxAge: -1})
	a.flashRedirect(w, r, "/dashboard/account", "Portfolio removed.")
}
