package app

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/config"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

// The 2026-09-06 verification's finding 4: the dashboard upload had no total request-size bound.
// ParseMultipartForm's argument limits what the parser keeps in memory — everything past it
// spills to a temp file — and the file itself was read through a plain LimitReader, which
// truncates silently. Padding a valid export past the limit therefore imported a file whose tail
// had been thrown away with nothing on screen to say so.
//
// The JSON API and the backup restore already had body caps; this is the route that did not, so
// it is the route the test drives.
func TestDashboardWatchlistImportRejectsAnOversizedFile(t *testing.T) {
	app := newWatchlistUploadTestApplication()

	// A complete, valid export followed by enough whitespace to pass the limit: truncation would
	// leave exactly this document, still parseable, and import it.
	payload := `{"items":[{"symbol":"TESTBIG","added_at":"2026-01-02T03:04:05Z"}]}` +
		strings.Repeat(" ", maxWatchlistImportBytes)
	recorder := postWatchlistImport(t, app, payload)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected the page to render an error, got %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "too large") {
		t.Fatalf("expected the upload to be refused as too large, got: %s", firstLines(recorder.Body.String()))
	}
	if items, _ := app.Watchlist.List(); len(items) != 0 {
		t.Fatalf("an oversized upload must import nothing, got %+v", items)
	}
}

// A file within the limit still imports — the bound must not break the feature it protects.
func TestDashboardWatchlistImportAcceptsANormalFile(t *testing.T) {
	app := newWatchlistUploadTestApplication()

	recorder := postWatchlistImport(t, app, `{"items":[{"symbol":"TESTOK","added_at":"2026-01-02T03:04:05Z"}]}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected the page to render, got %d", recorder.Code)
	}
	items, err := app.Watchlist.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Symbol != "TESTOK" {
		t.Fatalf("expected TESTOK to be imported, got %+v", items)
	}
}

func postWatchlistImport(t *testing.T, app *Application, payload string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("mode", "merge"); err != nil {
		t.Fatalf("write mode field: %v", err)
	}
	file, err := form.CreateFormFile("file", "watchlist.json")
	if err != nil {
		t.Fatalf("create file field: %v", err)
	}
	if _, err := file.Write([]byte(payload)); err != nil {
		t.Fatalf("write file field: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/watchlist/import", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	recorder := httptest.NewRecorder()
	app.watchlistImportPageHandler(recorder, request)
	return recorder
}

func newWatchlistUploadTestApplication() *Application {
	return &Application{
		Config:    config.Config{},
		Metrics:   observability.NewMetrics(),
		Logger:    zap.NewNop(),
		Watchlist: watchlistUC.NewUseCase(),
	}
}

func firstLines(s string) string {
	if len(s) > 400 {
		return s[:400]
	}
	return s
}
