# Go

- Clean Architecture, four layers: `internal/domain` (types + pure business rules, no I/O) →
  `internal/application` (use cases, orchestration) → `internal/infrastructure` (DB/cache/
  provider/notification adapters) → `internal/app` (HTTP handlers + `app.go`'s composition root).
  Business logic never lives in an HTTP handler — handlers parse the request, call a use case,
  shape the response.
- Dependency injection happens once, in `internal/app/app.go`'s `New()` — every use case is
  constructed there and wired with its real infrastructure adapters. No `init()`-time wiring, no
  package-level singletons.
- Every optional external dependency (market provider, ML model, Discord webhook, NBP exchange
  rates, admin auth) is nil-safe and non-fatal when unconfigured — the app always starts and
  degrades gracefully (deterministic stub prediction, no login gate, etc.) rather than refusing
  to boot. Only TimescaleDB/Redis connection failures are fatal at startup.
- Concurrency-shared state that changes after startup uses `atomic.Pointer[T]`, not a plain
  field guarded by convention — e.g. `ml.UseCase`'s `model`/`recoveryModel` fields, swapped live
  from the Account dashboard's model picker while `Predict()` runs concurrently on other
  requests. A plain pointer field there would be a real data race, not just poor style.
- Batch operations over independent items collect and return `errors.Join(errs...)` rather than
  aborting on the first failure — see `portfolio.UseCase.RemoveTransactions` (bulk-delete) and
  `Application.Close()` (releases DB/Redis/ONNX resources even if one fails).
- Error wrapping: always `fmt.Errorf("doing the thing: %w", err)`, never a bare error string —
  every error in this codebase should be traceable back through `errors.Is`/`errors.As`.
- Logging is `go.uber.org/zap`, structured fields only (`zap.String(...)`, `zap.Error(err)`), no
  `fmt.Println`/`log.Printf` in production code paths (a CLI tool printing its own output, like
  `cmd/generate-admin-credentials`, is a legitimate exception — it's not a server).
- Configuration is plain `os.Getenv` (`internal/config/config.go`), not Viper — this is a
  documented, deliberate deviation from `CODING_GUIDELINES.md`'s aspirational stack list, not an
  oversight; don't "fix" it without discussing first.
- No worker pools and no benchmark tests exist in this codebase today — don't assume either
  pattern is in use elsewhere as precedent.