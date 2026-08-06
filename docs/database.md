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

Implementation status: symbols, prices, portfolios, positions, transactions, watchlists,
watchlist_items, predictions, and recommendations are migrated and actively read/written
(migrations/001-004). indicators are migrated but nothing writes to them — those results are
computed fresh per request. predictions always gets `score`/`confidence`/`model_version` written (migrations/004);
`correction_probability` is also written when a real trained model produced it (see docs/ml.md),
staying NULL for the deterministic stub. `expected_duration`/`expected_strength` stay NULL
regardless — no model produces those yet. There is no alerts table.