package app

import (
	"errors"
	"net/http"
	"strings"
)

// sessionCookieName is the cookie holding a session token issued by authUC.UseCase.Login.
const sessionCookieName = "atlas_session"

// publicPaths are reachable with no session even when auth is Enabled — health/metrics are
// infra-facing (Kubernetes probes, Prometheus scraping) and shouldn't require a browser login,
// and /login is obviously how you get a session in the first place.
var publicPaths = map[string]bool{
	"/health":         true,
	"/metrics":        true,
	"/login":          true,
	"/dashboard/lang": true, // a display-language preference, not sensitive — usable pre-login too
	"/favicon.ico":    true, // every browser requests this on its own, including on the login page itself
}

// requireAuth gates every route except publicPaths behind a valid session — a no-op passthrough
// when a.Auth isn't Enabled() at all, matching Atlas's existing non-fatal-optional pattern
// (ML/Discord): unset ADMIN_USERNAME/ADMIN_PASSWORD_HASH means every route stays reachable with
// no login gate, exactly as before this feature existed. When enabled, "/" (the dashboard itself
// — see http_handler.go's NewHandler) and every /dashboard/* path (browser-facing HTML) redirect
// to /login on a missing/invalid session; every other path (the raw JSON API) gets a real 401
// instead — an API consumer following a redirect to an HTML login page would be far more
// confusing than a 401.
func (a *Application) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /static/* (Bootstrap CSS/JS) must load on the login page itself, before there's any
		// session at all.
		if !a.Auth.Enabled() || publicPaths[r.URL.Path] || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		if cookie, err := r.Cookie(sessionCookieName); err == nil && a.Auth.ValidateSession(r.Context(), cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/dashboard") {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
	})
}
