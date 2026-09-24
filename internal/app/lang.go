package app

import (
	"fmt"
	"net/http"

	"atlas/internal/app/web"
)

// langCookieName holds the dashboard's display-language preference (see web.Lang), kept for a
// long time since it is a UI preference.
const langCookieName = "atlas_lang"

// langCookieMaxAge is a year: a display preference, not a session, so it should stick around.
const langCookieMaxAge = 365 * 24 * 60 * 60

// resolveLang reads the caller's language preference from langCookieName, defaulting to
// web.DefaultLang when the cookie is absent or holds a value Atlas doesn't have translations
// for (e.g. a stale cookie from a removed language, or tampering).
func (a *Application) resolveLang(r *http.Request) web.Lang {
	cookie, err := r.Cookie(langCookieName)
	if err != nil {
		return web.DefaultLang
	}
	lang := web.Lang(cookie.Value)
	if !web.ValidLang(lang) {
		return web.DefaultLang
	}
	return lang
}

// langSetHandler sets the language cookie and redirects back to whatever page the switcher was
// submitted from (the nav's language dropdown, present on every dashboard page) — see
// templates/partials.html's "nav" block. Switching language needs a real page reload (unlike
// the theme switch, which is pure client-side CSS), since translated text is rendered
// server-side.
func (a *Application) langSetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	lang := web.Lang(r.FormValue("lang"))
	if !web.ValidLang(lang) {
		lang = web.DefaultLang
	}
	http.SetCookie(w, &http.Cookie{
		Name: langCookieName, Value: string(lang), Path: "/",
		MaxAge: langCookieMaxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	redirect := r.Referer()
	if redirect == "" {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// tr translates a handler's UI message into the request's display language, filling any
// fmt verbs from args — see the "msg.*" keys in web/i18n.go. Messages built from an error keep
// the error's own text, which comes from lower layers and is not translated.
func (a *Application) tr(r *http.Request, key string, args ...any) string {
	message := web.T(a.resolveLang(r), key)
	if len(args) == 0 {
		return message
	}
	return fmt.Sprintf(message, args...)
}
