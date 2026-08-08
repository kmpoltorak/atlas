GET /health
GET /symbols
GET /prices
GET /portfolio
POST /portfolio
DELETE /portfolio
GET /transactions
DELETE /transactions
GET /watchlist
POST /watchlist
DELETE /watchlist
GET /alerts
GET /recommendations
GET /predictions
GET /backtests
GET /backtests/history
GET /backtests/walkforward

**Implementation status**: this list covers the raw JSON API only (`internal/app/handlers.go`) —
see `internal/app/http_handler.go` for the full route table including the server-rendered
dashboard (`/dashboard/*`) and its form-submitted bulk-delete actions, which have no JSON API
equivalent. `DELETE /portfolio` clears a symbol's current position aggregate (`?symbol=`);
`DELETE /transactions` removes one historical transaction by id and recomputes the affected
position (`?id=`) — these are two different operations, not duplicates, matching
docs/decision_engine.md's Portfolio Engine distinction. `DELETE /watchlist` takes `?symbol=`.
