package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"atlas/internal/app/web"
)

func TestResolveLangDefaultsToEnglishWithNoCookie(t *testing.T) {
	a := &Application{}
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	if got := a.resolveLang(r); got != web.DefaultLang {
		t.Fatalf("expected default lang %q with no cookie, got %q", web.DefaultLang, got)
	}
}

func TestResolveLangReadsAValidCookie(t *testing.T) {
	a := &Application{}
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	r.AddCookie(&http.Cookie{Name: langCookieName, Value: "pl"})
	if got := a.resolveLang(r); got != web.LangPL {
		t.Fatalf("expected pl, got %q", got)
	}
}

func TestResolveLangRejectsAnUnsupportedCookieValue(t *testing.T) {
	a := &Application{}
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	r.AddCookie(&http.Cookie{Name: langCookieName, Value: "it"})
	if got := a.resolveLang(r); got != web.DefaultLang {
		t.Fatalf("expected an unsupported cookie value to fall back to %q, got %q", web.DefaultLang, got)
	}
}

func TestLangSetHandlerSetsCookieAndRedirectsToReferer(t *testing.T) {
	a := &Application{}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/lang", nil)
	req.Form = map[string][]string{"lang": {"pl"}}
	req.Header.Set("Referer", "/dashboard/portfolio")
	rec := httptest.NewRecorder()

	a.langSetHandler(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected a redirect, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/dashboard/portfolio" {
		t.Fatalf("expected redirect back to the referer, got %q", got)
	}
	resp := rec.Result()
	found := false
	for _, c := range resp.Cookies() {
		if c.Name == langCookieName && c.Value == "pl" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the %s cookie to be set to pl, got %+v", langCookieName, resp.Cookies())
	}
}

func TestLangSetHandlerRejectsAnUnsupportedLanguage(t *testing.T) {
	a := &Application{}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/lang", nil)
	req.Form = map[string][]string{"lang": {"it"}}
	rec := httptest.NewRecorder()

	a.langSetHandler(rec, req)

	resp := rec.Result()
	for _, c := range resp.Cookies() {
		if c.Name == langCookieName && c.Value != string(web.DefaultLang) {
			t.Fatalf("expected an unsupported language to fall back to %q, got cookie value %q", web.DefaultLang, c.Value)
		}
	}
}

func TestLangSetHandlerDefaultsRedirectWithNoReferer(t *testing.T) {
	a := &Application{}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/lang", nil)
	req.Form = map[string][]string{"lang": {"en"}}
	rec := httptest.NewRecorder()

	a.langSetHandler(rec, req)

	if got := rec.Header().Get("Location"); got != "/" {
		t.Fatalf("expected a fallback redirect to /, got %q", got)
	}
}

func TestLangSetHandlerRejectsNonPost(t *testing.T) {
	a := &Application{}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/lang", nil)
	rec := httptest.NewRecorder()

	a.langSetHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// Handler messages follow the display language like the rest of the page did already.
func TestHandlerMessagesFollowTheDisplayLanguage(t *testing.T) {
	a := &Application{}
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	r.AddCookie(&http.Cookie{Name: langCookieName, Value: "pl"})
	if got := a.tr(r, "msg.watchlist_added", "AAPL"); got != "Dodano AAPL do listy obserwowanych." {
		t.Fatalf("expected the Polish message with the symbol filled in, got %q", got)
	}
	if got := a.tr(httptest.NewRequest(http.MethodGet, "/", nil), "msg.portfolio_removed"); got != "Portfolio removed." {
		t.Fatalf("expected English without a language cookie, got %q", got)
	}
}
