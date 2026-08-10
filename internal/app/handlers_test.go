package app

import "testing"

func TestDecodeWatchlistImportAcceptsBothShapes(t *testing.T) {
	// The export shape, which must round-trip exactly.
	items, err := decodeWatchlistImport([]byte(`{"items":[{"symbol":"AAPL","added_at":"2026-01-01T00:00:00Z"}]}`))
	if err != nil {
		t.Fatalf("export shape: %v", err)
	}
	if len(items) != 1 || items[0].Symbol != "AAPL" || items[0].AddedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("export shape decoded to %+v", items)
	}

	// A hand-written list of tickers, which is the other realistic way this file gets made.
	items, err = decodeWatchlistImport([]byte(`["AAPL","MSFT"]`))
	if err != nil {
		t.Fatalf("bare list: %v", err)
	}
	if len(items) != 2 || items[1].Symbol != "MSFT" {
		t.Errorf("bare list decoded to %+v", items)
	}
	if items[0].AddedAt != "" {
		t.Errorf("a bare list carries no dates; Import stamps them, got %q", items[0].AddedAt)
	}
}

func TestDecodeWatchlistImportRejectsJunk(t *testing.T) {
	for name, body := range map[string]string{
		"empty":        ``,
		"whitespace":   "  \n ",
		"not json":     `hello`,
		"wrong object": `{"symbols":["AAPL"]}`,
		"truncated":    `{"items":[`,
		"scalar":       `42`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeWatchlistImport([]byte(body)); err == nil {
				t.Errorf("expected %q to be rejected", body)
			}
		})
	}
}
