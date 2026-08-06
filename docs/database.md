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
computed fresh per request. predictions only gets its `score` column (migrations/004) written;
the richer target-design columns (correction_probability, expected_duration, expected_strength)
stay NULL until a real trained model exists. There is no alerts table.