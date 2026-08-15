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
GET /watchlist/export
POST /watchlist/import
GET /alerts
GET /recommendations
GET /predictions
GET /backtests
GET /backtests/history
GET /backtests/walkforward

Interactive, always-current documentation for every route above — request/response schemas,
"Try it out" against the real running instance — is served at `/swagger/index.html`
(`swaggo/swag` annotations on each handler, see `internal/app/handlers.go`). It's not in
`publicPaths`, so it needs the same session as the API itself when auth is enabled; log into the
dashboard first and "Try it out" reuses that session's cookie automatically.

**Implementation status**: this list covers the raw JSON API only (`internal/app/handlers.go`) —
see `internal/app/http_handler.go` for the full route table including the server-rendered
dashboard (`/dashboard/*`) and its form-submitted delete actions, which have no JSON API
equivalent. Each of those takes one row or a checkbox-selected batch on the *same* endpoint (a
repeated field with a single-value fallback — see `web_handlers.go`'s `formValues`), so there is
no separate bulk route to look for. `DELETE /portfolio` clears a symbol's current position
aggregate (`?symbol=`);
`DELETE /transactions` removes one historical transaction by id and recomputes the affected
position (`?id=`) — these are two different operations, not duplicates, matching
docs/decision_engine.md's Portfolio Engine distinction. `DELETE /watchlist` takes `?symbol=`.

`GET /watchlist/export` returns the same `{"items":[...]}` body as `GET /watchlist`, with a
`Content-Disposition` header so a browser saves it as a file. `POST /watchlist/import` accepts
that exact shape back — so an export from one Atlas instance imports into another with no
conversion — and also accepts a bare array of tickers (`["AAPL","MSFT"]`) for a hand-written
list. `?mode=replace` mirrors the file exactly, untracking symbols it omits; any other value,
including an absent one, merges. Merge is the default deliberately: the destructive mode always
has to be asked for by name. The response reports what actually happened
(`{"added":N,"skipped":N,"removed":N}`), not just a status.
