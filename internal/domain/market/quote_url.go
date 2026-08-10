package market

import (
	"net/url"
	"strings"
)

// QuoteURL is the public quote page for a ticker, used wherever Atlas shows a symbol to a human:
// the dashboard tables link every symbol to it, and Discord alerts hang their title on it.
//
// It points at Yahoo Finance because that is where Atlas' own bars come from (see
// infrastructure/providers.YahooFinanceProvider), which is what makes it safe: a ticker Atlas can
// collect is by construction a ticker this page resolves, exchange suffixes and all — GDX,
// PKN.WA. A different provider would need a different mapping, not just a different host, so this
// deliberately does not pretend to be provider-agnostic.
//
// PathEscape guards the dots and hyphens real exchange suffixes carry from being read as path
// segments. An empty symbol yields the bare quote path rather than an error: callers are
// rendering a link next to a symbol they already have, and none of them can act on a failure
// here.
func QuoteURL(symbol string) string {
	return "https://finance.yahoo.com/quote/" + url.PathEscape(strings.TrimSpace(symbol)) + "/"
}
