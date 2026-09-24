# REST API

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | Liveness plus per-dependency status |
| GET | `/metrics` | Prometheus exposition |
| GET | `/symbols` | Known symbols |
| GET | `/prices` | Fetch and collect OHLCV history for a symbol |
| GET, POST, DELETE | `/portfolio` | Positions and risk / record a transaction / hide a position |
| GET, DELETE | `/transactions` | Transaction history / delete one and recompute its position |
| GET, POST, DELETE | `/watchlist` | Tracked symbols |
| GET | `/watchlist/export` | Download the watchlist as a JSON file |
| POST | `/watchlist/import` | Apply an exported watchlist |
| GET | `/alerts` | Current non-HOLD signals from the background poll |
| GET | `/recommendations` | Run the decision engine for a symbol |
| GET | `/predictions` | Raw ML output for a symbol |
| GET | `/backtests` | Run a historical simulation |
| GET | `/backtests/history` | Past backtest runs |
| GET | `/backtests/walkforward` | Walk-forward threshold optimization (report only) |

There is no authentication: every port is bound to `127.0.0.1` ([deployment.md](deployment.md)).

Interactive documentation for every route — request/response schemas, "Try it out" against the
running instance — is served at `/swagger/index.html` (`swaggo/swag` annotations on each handler,
see `internal/app/handlers.go`), with the JSON specification at `/swagger/doc.json`. The
specification is generated Go code in `internal/swagger/docs.go`, compiled into the binary. Never
edit it by hand; regenerate it after changing handler annotations or response types:

```sh
go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/api/main.go --output internal/swagger \
  --outputTypes go --packageName swagger --parseDependency --parseInternal
```

## Notes on specific routes

The server-rendered dashboard (`/dashboard/*`, route table in `internal/app/http_handler.go`) has
form-submitted actions with no JSON equivalent. Each delete action takes one row or a
checkbox-selected batch on the *same* endpoint (a repeated field with a single-value fallback,
`web_handlers.go`'s `formValues`), so there is no separate bulk route.

`POST /portfolio` accepts an optional `id`. Recording an id that already exists with the same data
is a no-op, so a retried request records one transaction.

`DELETE /portfolio?symbol=` and `DELETE /transactions?id=` are different operations. The first
hides a position: it clears the current aggregate but keeps the transactions, so the next
transaction on that symbol rebuilds the position from all of them. The second deletes one
transaction and recomputes the position from what remains; it is refused if what remains would
sell more than was bought (delete the SELL first).

`GET /watchlist/export` returns the same `{"items":[...]}` body as `GET /watchlist`, with a
`Content-Disposition` header so a browser saves it as a file. `POST /watchlist/import` accepts
that shape back — so an export from one Atlas instance imports into another with no conversion —
and also a bare array of tickers (`["AAPL","MSFT"]`) for a hand-written list. `?mode=replace`
mirrors the file exactly, untracking symbols it omits; any other value, including none, merges.
Merge is the default deliberately: the destructive mode always has to be asked for by name. The
response reports what happened (`{"added":N,"skipped":N,"removed":N}`).
