# Database
Tables
symbols
prices
portfolios
positions
transactions
watchlists
watchlist_items
indicators
predictions
recommendations
Indexes
ticker
timestamp
symbol_id
Use hypertables.
Enable compression.
Retention policy:
10 years.

---

Implementation status: symbols, prices, portfolios, positions, transactions, watchlists, and
watchlist_items are migrated and actively read/written (migrations/001-003). indicators,
predictions, and recommendations are migrated but nothing writes to them yet — those results
are computed fresh per request. There is no alerts table.