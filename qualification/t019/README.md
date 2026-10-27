# T019 SQLite driver qualification artifacts

- `capture.txt` — commands, exits, versions, frozen-fixture digests (recorded 2026-10-27 UTC).
- `bdd-red.txt` — pre-implementation red run (BDD-first evidence: suite failed to build because `internal/storage` did not exist; after the package existed, scenarios were run to red before turning green).
- Receipt: `receipts/t019/attempt-001.json` (schema `t019-qualification-receipt-v1`).

Driver qualified: **modernc.org/sqlite v1.61.0** (pure Go, CGO-free), SQLite **3.54.1**,
WAL / foreign_keys=ON / synchronous=NORMAL / busy_timeout=10000 recorded live.
