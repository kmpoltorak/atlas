# Coding Guidelines
Language:

Go

Architecture:

Clean Architecture

Patterns:

Repository

Factory
Strategy

Logging:

Zap

Configuration:

Viper

Database:

sqlx

Rules

No panic()

No fmt.Println()

No global variables

No SELECT *

Always use context.Context

Wrap errors

Return typed errors

Small interfaces

Small packages

Small commits

Write benchmarks for critical code.

---

Implementation status: Configuration currently uses plain os.Getenv (internal/config), not
Viper — no functional gap, but this guideline isn't followed yet. No Benchmark tests exist
yet. Of the named patterns, only Repository is real (portfolio.Repository, watchlist.Repository,
etc.) — Factory/Strategy were aspirational and never took the GoF shape; what the codebase
actually leans on instead is plain constructor functions (NewUseCase, NewOrchestrator) plus
interface-based swapping (market.MarketProvider, notifications.Notifier), which covers the same
ground without the pattern names. panic() and fmt.Println() are followed with one deliberate
exception each: cmd/generate-admin-credentials prints its result (a real CLI tool's actual
output, not debug logging), and persistence/backup.go's restore path uses `SELECT *` against a
json_populate_recordset, not a live table — neither is the anti-pattern these rules guard
against. Zap, sqlx, no global variables, wrapped/typed errors are all genuinely followed.
