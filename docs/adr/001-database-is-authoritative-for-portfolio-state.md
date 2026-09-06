# 001 — The database, not process memory, is authoritative for portfolio state

**Status**: Accepted (2026-09-05)

## Context

`portfolio.UseCase` held positions and transactions in memory and wrote them back to the database
per mutation. Reads served the in-memory copy. This was fine for exactly one process, and only
because nothing else wrote.

Two consequences the 2026-09-05 review found:

- A transaction and its position update were separate SQL statements, each committed on its own.
  A failure of the second left the transaction recorded and the position not, and the client saw
  an error — so a retry could double it.
- A write started from an in-memory snapshot could overwrite a newer state. With one process this
  needed a race between two requests; with two replicas it needed nothing at all.

## Decision

The database is the single authority.

- Writes go through `persistence.MutatePortfolio`: take a row lock on the portfolio
  (`SELECT … FOR UPDATE`), read current positions and transactions, apply a pure function to
  them, write both back, commit once. The in-memory copy is updated only after the commit
  succeeds.
- Reads re-read. `portfolio.UseCase.ForPortfolio` builds a request-owned view hydrated from the
  database, so no request serves another request's snapshot.
- Every transaction carries a caller-visible UUID, making a retry idempotent instead of doubling.
- The invariants the application enforces are also `CHECK` constraints (`migrations/018`), so a
  restore or a manual `psql` session cannot write what the application believes impossible.

## Consequences

Each portfolio request now costs a query it did not before. That is the price, and it is small:
these are indexed reads of a few dozen rows.

The in-memory fields remain as the no-database fallback for tests and a degraded start. That
duality is the part most likely to rot — a future writer could reasonably conclude the in-memory
copy is still authoritative. If that becomes a real problem, the fix is to delete the fallback and
require a repository.

## What would make this wrong

If Atlas ever needs to serve portfolio reads faster than the database can answer them. It does
not: one user, a few hundred rows.
