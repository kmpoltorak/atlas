package app

import (
	"net/http"
	"net/url"
)

// flashCookieName carries a one-time success message across a POST-then-redirect. Short-lived
// and read-and-cleared by the very next request (see readFlash) — never a stored preference like
// langCookieName/portfolioCookieName.
const flashCookieName = "atlas_flash"

// flashRedirect is the fix for a POST handler that used to render its success response directly:
// rendering directly leaves the browser's current "page" as the result of a POST, so hitting
// refresh resubmits it — confirmed live as mlTrainHandler silently starting a second (then third)
// training run on every page refresh. Redirecting turns the location bar into a GET, which a
// refresh just re-fetches; the flash cookie carries the success message across that redirect so
// it still shows up on the page the user lands on.
func (a *Application) flashRedirect(w http.ResponseWriter, r *http.Request, path, message string) {
	if message != "" {
		http.SetCookie(w, &http.Cookie{
			Name: flashCookieName, Value: url.QueryEscape(message), Path: "/",
			MaxAge: 30, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// readFlash returns and clears any flash message set by flashRedirect. Callers only need this
// when successMsg wasn't already supplied some other way (see renderAccountPage).
func readFlash(w http.ResponseWriter, r *http.Request) string {
	cookie, err := r.Cookie(flashCookieName)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookieName, Value: "", Path: "/", MaxAge: -1})
	message, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		return ""
	}
	return message
}
