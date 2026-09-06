# Architecture Decision Records

Short records of decisions that are not obvious from the code, and that someone will otherwise
re-litigate. Each one states what was decided, what it costs, and what would make it wrong.

They exist because the 2026-09-05 review found the opposite pattern: long comments narrating the
history of individual fixes, sitting next to declarations ("stateless", "atomic import") that the
implementation had since contradicted. A decision belongs in one place, dated, with its trade-off
written down — not spread across the comments of the files it happens to touch.

Format: one file per decision, `NNN-short-title.md`, never edited after acceptance except to add
a "Superseded by" line. Getting it wrong later is normal; quietly rewriting what was decided is
what makes the record useless.

| # | Decision | Status |
|---|---|---|
| [001](001-database-is-authoritative-for-portfolio-state.md) | The database, not process memory, is authoritative for portfolio state | Accepted |
| [002](002-one-leader-polls-not-every-replica.md) | One leader polls; every replica serves | Accepted |
| [003](003-alert-delivery-is-an-outbox.md) | Alert delivery is a durable outbox | Accepted |
| [004](004-backtests-execute-at-the-next-open.md) | Backtests signal on closed bars and fill at the next open | Accepted |
| [005](005-no-fabricated-numbers.md) | Absent data is reported as absent, never substituted | Accepted |
