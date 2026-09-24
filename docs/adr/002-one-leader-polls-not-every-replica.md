# 002 — One leader polls; every replica serves

**Status**: Accepted (2026-09-05)

> **2026-09-24:** the Kubernetes manifests this context describes were removed; Atlas now runs
> as one `atlas-api` process under Docker Compose ([deployment.md](../deployment.md)). The lease
> still decides which process polls, which is what keeps an overlapping restart from polling
> twice.

## Context

`deploy/deployment.yaml` ran two replicas and the HPA scaled to six, while the code assumed one
process. Each replica ran its own alert-poll loop: each evaluated the whole watchlist, each wrote
its own recommendations, and each sent its own copy of every notification. `docs/deployment.md`
described the API as "Stateless", which it was not.

The short-term fix was to drop to one replica. That is not a scaling story, it is a moratorium.

## Decision

Separate the work that must happen exactly once from the work every replica can do.

- **Once**: evaluating the watchlist, writing recommendations, sending notifications, pruning old
  data. Gated on a database lease (`leader_election`, `app.Leader`).
- **Every replica**: serving HTTP, reading state, refreshing runtime settings from the database.

The lease is a lease, not a lock: it carries an expiry, renewed at a third of its TTL. A process
that dies holding it blocks nobody — the lease simply expires and another replica takes it. A
clean shutdown releases it immediately.

Acquisition is one atomic `INSERT … ON CONFLICT DO UPDATE … WHERE holder = $1 OR expires_at <
now()`. Read-then-write would let two processes both observe an expired lease and both conclude
they had won it.

## Consequences

An unclean death costs up to one lease TTL (two minutes) with nobody polling — under one 300s
poll cycle, so at most one skipped poll.

The holder id is per-process, not the hostname: two pods can share a hostname, and a restarted
process must re-acquire rather than inherit its own previous lease.

This is not a general-purpose distributed lock, and should not become one. It coordinates one
singleton loop. Anything needing real consensus needs a real consensus system.

## What would make this wrong

If background work grew past what one replica can do in a poll interval. Then the answer is
partitioning the watchlist across workers, not several leaders.
