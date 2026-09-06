# 003 — Alert delivery is a durable outbox

**Status**: Accepted (2026-09-05)

## Context

Undelivered notifications lived in a `map[string]*pendingAlert` in the poll loop. It recorded,
per symbol, which channels still owed a delivery after a partial failure — good, and it died with
the process.

Worse than losing retries: the map only existed *after* a send had been attempted. A process that
died between deciding to notify and notifying left nothing at all, and a restart could not tell
"delivered" from "never tried". `seedLastActions` restored what the engine last *said*, which is
not the same claim as what was *sent*, and its own comment said so.

## Decision

Record before sending.

`alert_outbox` holds one row per action transition worth notifying about, written before any
channel is contacted, with `pending_channels` listing the channels that still owe it. Delivery
narrows that list; the row is delivered when it empties. A restart picks up whatever is left.

Sending stays best-effort per channel: one channel's failure neither blocks the others nor
re-notifies one that already succeeded — the same guarantee the in-memory map gave, now across
process lifetimes.

Channels are identified by name (`Notifier.Name`), not by pointer. A pointer means nothing after
a restart.

## Consequences

An extra table and an extra write per notified transition. Trivial against the volume: alerts are
action *changes*, not polls.

The outbox is a delivery mechanism, not an audit log — `recommendations` already holds what the
engine said. Delivered rows are pruned.

A payload that cannot be decoded is marked delivered with the error recorded, rather than retried
forever. Nothing will ever make it deliverable, and a poison entry retried every 30 seconds is
just noise.

## What would make this wrong

If alert delivery ever needs ordering guarantees or exactly-once semantics across channels. It
does not: these are advisory notifications, and at-least-once with per-channel deduplication is
the right shape.
