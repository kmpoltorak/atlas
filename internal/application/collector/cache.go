package collector

import "atlas/internal/domain/market"

// Cache is a short-lived, best-effort store in front of the live market data provider. It
// exists to reduce redundant calls to volatile, rate-limit-sensitive external providers when
// multiple handlers request the same symbol/window in quick succession (e.g. /recommendations
// and /predictions both collecting the same symbol). A cache miss or write failure is never
// an error here — a broken cache should degrade to always calling the live provider.
type Cache interface {
	Get(symbol string, window int) ([]market.Bar, bool)
	Set(symbol string, window int, bars []market.Bar)
}
