---
id: adr-session-lease-liveness
title: ADR - Session Lease Ownership and Liveness
type: adr
status: accepted
adr_group: Go CLI
created: 2026-11-25
modified: 2026-11-25T12:00
deciders:
  - Engineering Team
consulted:
  - Architecture Review Board
informed:
  - FlowState Contributors
tags:
  - type/adr
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/session
  - topic/lease
  - topic/recovery
  - flowstate/cli
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Lease Liveness
  - Session Lease Ownership
---

# ADR - Session Lease Ownership and Liveness

## Status

**Accepted** — 2026-11-25. Decision only: no code changes with this ADR. It replaces the "one process runs a session
at a time" assumption of ADR-009 D8 and ADR-006's rejection of leases for sessions (Alternative B) with an enforced
rule, and it is the design the `session compact` change (#458, merged) has been waiting for. The code facts below were
read at `b8b7086e` (which includes #458) and rechecked at `0a6784d9`; the review history is in the pull
request, not here.

## Context

A session's event log has one writer at a time, and nothing enforces that across processes. What the code does today:

- **`session run` takes the exec lease under a fixed name.** `RunSessionExecLoop` and the swarm coordinator
  (`runSwarmCoordinator`) both set the lease item to `exec:<session id>` and the owner to the constant `cli`
  (`execLeaseAgent`). `Store.AcquireLease` re-grants a lease the same agent already holds, token unchanged. A second
  `session run` on the same session, in another process, is therefore granted the lease of a live run: two writers.
- **Recovery cannot tell a crash from a live run.** `cmdRun` and `serveRun` call `RecoverCrashedSession` before the
  loop takes the lease. Its own documentation says it cannot tell a dead run from a live one, so a second run appends
  `session.interrupted` and answers `delegate` calls under a live run (ADR-009 D8 records the same limit).
- **The only cross-process guard is by accident.** The event-writer registry is a package variable, so it is per
  process. Across processes, the unique `(session, seq)` row turns two appends of the same seq into
  `ErrSeqCollision`, which is a late failure of one writer, not an exclusion. Nothing excludes interleaving when the
  two writers pick different seqs, and I did not run two real processes to see which outcomes occur.
- **A crashed run leaves its lease row; a lease never expires.** Today only the next `session run` reclaims it, and
  only because it uses the same name `cli`. `Store.ClearLeaseOwnedBy` exists and recovery uses it for the leases of
  a crashed parent's delegate children (owners `delegate:<run id>`, or exactly `delegate`, the name an earlier build
  used).
- **Delegation is already per-run.** `newDelegateOwner` gives every delegated run the owner `delegate:<uuid>`, for the
  reason the code states: a lease is re-granted to its own owner, and one shared name would let two runs of a child
  both hold it.
- **Fencing is thinner than it looks.** `FenceLease` is called once in `acquireExecLease`, before each provider
  request in the loop (`beginExecRequest` in `execloop.go`, which the overflow retry in `compacttrigger.go` calls too)
  and once before the append of a compaction (`compact.go`). `EventWriter.Append` takes no token. A stale holder that
  has passed the check can still append during its turn, so a token bump alone does not make a false takeover safe for
  the database writes.
- **A held lease is an `internal` error.** The loop wraps `ErrLeaseHeld` as `ErrExecLoopFenced`, which
  `app.ErrorCode` does not name, so the CLI reports code `internal` (exit 1, the default arm of `exitCodeFor`) and
  the HTTP server answers 500.
- **The text-only turn takes no lease.** `session run` without a tool surface calls `TurnWithOptions`, which opens
  an event writer and no lease. So do `ResumePausedSession`, `DrainSteeringMessages`, `session close`,
  `session cancel` and `tool run`.

The delegated-child resume of ADR-009 D8 shows the cost of the missing rule: its documentation says a held lease
means "the child is running, or its runner was killed with its process and left a stale lease". The two cannot be
told apart.

PR #458 (merged; read in `internal/app/compact.go`) found the live-run version of this: `session compact` shared the
owner `cli`, ran recovery under the live run and released the live run's lease. `CompactSession` now takes its own
owner, `compact:<uuid>` (`newCompactOwner`, since removed), and `AcquireLease` refuses any row of another owner, so a
session that has a `cli` or `run:` row, live or crashed, is refused with nothing written (as `CompactLeaseHeld`, which
matches `ErrExecLoopFenced`, so it too reaches the user as `internal`). That is a conservative patch, not a rule: a
crashed session must be resumed with `session run` before it can be compacted. A crashed compaction leaves a
`compact:<uuid>` row that nothing would clear, so `clearCompactLeases` deleted rows of that prefix at the start of
`CompactSession` and in `RecoverCrashedSession`. That clearing was the interim this ADR's liveness rule superseded:
packet 12 removed it, and a crashed judged `compact:<uuid>` row is now taken over like any other.

What the other harnesses do:

- **Pi** (its documentation, `sessions.md` and `session-format.md`). Sessions are JSONL files, one entry per line,
  with an append-only edit entry for context changes. I searched both files, and `how-pi-works.md`, for a locking or
  concurrency statement (lock, concurrent, multiple processes) and found none, so Pi's documentation makes no
  promise either way. Nothing here is taken from Pi.
- **Claude Code and OpenCode** (recalled, not verified): I do not recall either documenting a per-session lock, and
  none of this ADR depends on them.

So FlowState designs this itself, from what the code has: a lease table, fencing tokens, and an append-only log that
recovery reads.

## Decision

**Every writer of a session's log runs under an exec lease whose owner is unique to the run, and a lease is taken
from a holder only when the operating system says the holder's process is gone.** The liveness test is an advisory
lock on a per-session lock file that the kernel releases when its holder dies. The lease row records who holds it, so
a refusal can name the holder. Recovery runs only after ownership is established.

### Run identity

- Every run owner is unique per run: `run:<uuid>` for `session run` and for a serve run (ids from `session.NewID`, as
  `newDelegateOwner` does), `compact:<uuid>` for compaction, and `delegate:<run id>` as today. The swarm
  coordinator, which runs the loop on its coordinator session, uses `run:<uuid>` too.
- The lease row gains three columns: `machine` (text, a keyed hash of the machine id, never the raw id; see below),
  `host` (text, the host name at acquisition, **for display only**) and `pid` (integer). `agent`, `token` and
  `acquired_at_utc` stay.
  They are for judging and for the refusal message; the lock file, not the row, says whether the holder is alive. A
  **process start time is not recorded**: it exists to guard PID reuse, and no part of the decision compares a pid
  with the process table.
- **Stable machine identity.** Host names are unstable: macOS and DHCP names change with the network, every
  `docker run` gets a new one, and a machine can be renamed after a crash. Comparing them would make `unlock --force`
  routine. The lock already proves liveness on this machine; the only job of the machine check is to refuse to judge
  a row written on another machine, where a local lock proves nothing. So the identity is read from the system and
  the host name is never compared:
  - Linux: the contents of `/etc/machine-id`, then `/var/lib/dbus/machine-id` (recalled, not verified; the first
    exists on the machine I work on).
  - macOS: the hardware UUID, `IOPlatformUUID` (recalled), read by `sysctl kern.uuid` or `ioreg` (the packet picks
    the one the standard library can reach without a subprocess; recalled, unverified).
  - Windows: `MachineGuid` under `HKLM\SOFTWARE\Microsoft\Cryptography` (recalled), read with
    `golang.org/x/sys/windows/registry` (recalled, unverified).
  - **Fallback**: when none can be read (a minimal container has no `/etc/machine-id`), the identity is
    `host:<host name>`. That is the old comparison, with its instability, confined to those machines.
  The packet must verify each of these reads on its platform before relying on it.
- **The raw id is never stored, printed or logged.** `machine-id(5)` (read with `man machine-id` on this machine) says
  the id "should be considered "confidential", and must not be exposed in untrusted environments", that it "must not
  be used directly", and that it "should be hashed with a cryptographic, keyed hash function, using a fixed,
  application-specific key". The database is backed up, copied and attached to bug reports, so the column holds
  `hex(HMAC-SHA256(key = the machine id, message = "flowstate-lease-v1"))`. `machine-id(5)` asks only for "a
  cryptographic, keyed hash function, using a fixed, application-specific key" and names
  `sd_id128_get_machine_app_specific(3)` as an implementation. That page (read with `man` on this machine) says the
  function calls `sd_id128_get_app_specific()` with the machine id, and that this "calculates HMAC-SHA256 of the
  app_id parameter keyed by the base parameter": the key is the machine id and the message is the application id, as
  here (`flowstate-lease-v1` plays the application id; it is a fixed string, not a systemd 128-bit id, and the value
  is not truncated to a UUID). The precedent is that page, not the key wording of `machine-id(5)`. The hash
  is stable per machine and distinct from the id, so the comparison works and the id cannot be recovered from the row.
  The same holds for the macOS and Windows identities. The `host:<name>` fallback is stored as is: a host name is not
  confidential, and the host column shows it anyway. The refusal message prints the host, never `machine`.
- **Who writes them.** Only the new `AcquireRunLease` (see the rule) writes `machine`, `host` and `pid`. The existing
  `Store.AcquireLease` (legacy: the coordinator, delegate children, and every other caller until its packet moves it;
  `session compact` moved in packet 12) never writes them: its rows keep `machine = ''`, which the judgement table reads
  as unjudgeable and refuses. This is what makes the packet order safe (rule 10), so no packet may make the legacy
  acquire record a machine.
- **Migration.** Schema version 10. The migrator adds the columns and records version 10.
  - **Constants.** `foreignKeyVersion` stays `int64(9)`: it is the version that introduced the foreign keys (#426), and
    `migrateForeignKeys`, `backupExistingDatabase` and `backupTaken` in `fkrebuild.go` mean exactly "below v9".
    A new `leaseLivenessVersion = int64(10)` sits beside it in `fkmigrate_types.go`, and `knownSchemaVersion`, which is
    `= foreignKeyVersion` today, becomes `= leaseLivenessVersion`. `migrate` records 10 after the foreign-key step, and
    its early return for `maxApplied >= foreignKeyVersion` records 10 when `maxApplied < 10`.
  - **Consequence for the tests.** The four places in `info_test.go` that compare a migrated database's `SchemaVersion`
    to `foreignKeyVersion` now compare to `knownSchemaVersion`; the literal 9 in `migrate_test.go`, `fkmigrate_test.go`
    and the two `storeinfo_test.go` files becomes 10. The `.pre-v9.bak` assertions do not change, because the v9 backup
    is still taken for the same databases.
  - **Columns.** Every `leaseRow` column is `not null` (`orm_types.go`) and SQLite refuses
    `ALTER TABLE ... ADD COLUMN x text not null` without a default ("Cannot add a NOT NULL column with default value
    NULL", seen in `sqlite3`), so the new columns are declared `not null default ''` (`machine`, `host`) and
    `not null default 0` (`pid`), which is also what makes an old row read as "no machine". AutoMigrate adds missing
    columns on every open, before the version is read, and never drops or rewrites rows; so a database marked v10 by
    the version-only packet (see the packets) gains its columns in the next one, with no gap.
  - **Barrier.** An older binary then refuses the database with `ErrFutureSchema`, the downgrade barrier that has been
    in `migrate.go` since #41 (`9c3d7756`, the explicit m1 composition root; kept through the GORM migrator in #308).
  - **Backup naming.** `.pre-v9.bak` keeps its meaning and name: a database below v9 that is about to be rebuilt, as
    today. A database that is at v9, or at v8 with every key declared (the case that takes no backup today), is about
    to be changed only by v10, and takes `<db>.pre-v10.bak` first. One open takes at most one backup: a pre-v9
    snapshot also precedes the v10 change of the same open. The v9 code hard-codes the name (`backupPath` in
    `fkrebuild.go`) and its crash-safe copy (temporary file, exclusive create, hard link, `ErrBackupExists`,
    `ErrMigratedElsewhere`); the packet **generalises** it, passing the target version to `backupPath`,
    `backupBeforeRebuild` and `backupTaken` (whose test `v >= foreignKeyVersion` becomes "v >= the backup's target
    version"), and does not duplicate it. The two error messages in `errors.go` ("pre-v9 backup already exists",
    "migrated the database to v9") become version-neutral; the specs match the sentinels with `MatchError`, not the
    text. The backup is cheap insurance for a barrier that otherwise has no way back; the migration itself does not
    need it.

### Liveness: the options

| | (a) pid + start time | (b) heartbeat + TTL | (c) OS advisory lock on a lock file | (d) status quo |
|---|---|---|---|---|
| Needs | Per-OS code: `/proc/<pid>/stat` on Linux, `sysctl` on macOS and BSD, `OpenProcess` and `GetProcessTimes` on Windows | A column renewed between turns, a TTL, a clock | `flock` (Linux, macOS); `LockFileEx` (Windows); a lock file next to the DB | Nothing |
| Dead holder detected | If the pid is gone or its start time differs | If the last beat is older than the TTL | The kernel frees the lock at process exit, however it exits | Never |
| False "dead" (takeover of a live run) | A pid from another pid namespace looks dead; a clock-free check, but it reads the wrong table | A provider call or a suspended machine longer than the TTL; clock skew | None when the lock key is the file's identity and the holder shares the lock directory; a DB-only bind mount seen from another mount namespace, with its own lock directory, still defeats it (residual) | None |
| False "alive" (refusal of a dead run) | PID reuse without the start-time guard | A TTL longer than the stall | A child process that inherited the lock descriptor and outlived the run; a lock file deleted and recreated | Every crash |
| Different host | Cannot judge | Can judge by the clock, which is the weak part | Cannot judge unless the filesystem implements locks across hosts | Cannot judge |
| New dependency | `golang.org/x/sys` for darwin and windows probably | None | `golang.org/x/sys/windows` for `LockFileEx` | None |

The dependency facts: `go.mod` lists `golang.org/x/sys v0.49.0` as `// indirect` and no Go file in the repository
imports it, so using it directly is a `go.mod` edit and no new module. `syscall.Flock` exists for Linux and macOS;
`LockFileEx` is not in the standard `syscall` package on Windows but is in `golang.org/x/sys/windows` v0.49.0. I did
not check which of the calls (a) needs the standard library covers. The repository's CI runs the unit tests on
Linux, macOS and Windows, and `make preflight` vets for windows and darwin, so per-OS code is covered by gates.

**Decision: (c).** It is the only option whose failure to detect death is not a function of a clock or a process
table, and it errs toward the safe side: a lock that cannot be judged is refused, never taken. (b) is rejected
because its false "dead" is the harmful one (a live run taken over) and, as above, fencing does not make that safe.
(a) is rejected because pid namespaces make a live process in another container look dead. (d) is the bug.

A heartbeat is not added now; it is open question 2.

### The rule

1. **Lock identity.** The key is the identity of the database file, not the `--db` string. `store.Open` keeps the
   path as given (`store.go`, `dsn.go`), so `./f.db`, an absolute path and a symlink to the file would otherwise
   get three lock directories that all read "free", and a live run would be judged dead: the harm this ADR exists to
   avoid. The acquire therefore:
   1. resolves the path: `filepath.Abs`, then `filepath.EvalSymlinks` of the database file;
   2. stats the resolved file and reads its device and inode (`os.Stat(...).Sys()` as `*syscall.Stat_t` on Linux and
      macOS); on Windows the volume serial number and the file index, from `GetFileInformationByHandle` on an open
      handle (recalled; the packet must verify it, and that `os.Stat` alone does not expose them);
   3. puts the lock directory beside the resolved file, `<resolved path>.locks/`, and names each lock file by a hash
      of the identity and the item, in a subdirectory named by the first two hex digits of that hash (a fan-out, so
      a directory does not grow without bound; one file per session and per delegate child). No id can walk out of
      the directory.
   Identity also separates a database that was deleted and recreated at the same path from its predecessor's stale
   lock files. **Hard links** are not covered by the directory (the two paths resolve differently) and are outside
   what the store supports: SQLite names the `-wal` and `-shm` files from the path it was opened with, so two names
   for one file get two WAL pairs (recalled from SQLite's documentation, not verified here). **A DB-only bind mount**
   into another mount namespace keeps the device and inode, but a process there that does not see the same lock
   directory gets another lock: the residual, stated rather than closed. A volume that carries the database and its
   `.locks` directory together is judged correctly.
2. **Why the database file itself is not the lock file.** SQLite takes its own locks on the database file (POSIX
   byte-range locks on Unix, as I recall; mandatory `LockFileEx` ranges on Windows). A whole-file lock of ours would,
   on Windows, make SQLite's own reads and writes fail while a run holds it, and on Unix the process-wide semantics
   of POSIX locks (closing any descriptor of the file drops the process's locks) interact with the connection pool's
   opening and closing of that file. A separate lock file keeps the two lock systems apart. FlowState never deletes a
   lock file while a run may hold it; a prune is feasible later (lock the file, compare descriptor with path, unlink,
   unlock) and is not part of this ADR.
3. **Path-less stores.** `store.OpenMemory()` has `s.path == ""` (`store.go`); `Dependencies.Store` is documented
   as taking it for tests (`internal/app/types.go`; `ExecLoopConfig.Store`, in `execloop_types.go`, carries no such
   comment). Counted at `b8b7086e` (the only commit these counts refer to): 20 test files use `OpenMemory`; 8 call
   `RunExecLoop` or `RunSessionExecLoop` directly (`delegate_test`, `delegatenest_test`, `delegateparallel_test`,
   `delegateresume_test`, `delegatetool_test`, `execloop_session_test`, `execloop_test`, `compacttrigger_test`), and
   3 of those (`delegate_test`, `execloop_session_test`, `execloop_test`) open the in-memory store themselves. A store
   without a path uses a **process-local lock table keyed by item**: in-process exclusion only, no file, no identity.
   The specs say so: a spec on the in-memory store proves exclusion inside one process and nothing about processes.
   `--db :memory:` is **not** path-less: `fileDSN` prefixes a relative path with `./` so that the name `:memory:` does
   not open an in-memory database (`dsn.go` comment), so it is a file named `./:memory:` and takes the file rule.
4. **Lock file.** Holding an exclusive, non-blocking lock on the file for the whole run is what "alive" means. The
   file's contents mean nothing. The descriptor is opened close-on-exec (as I recall, Go's `os.OpenFile` does this
   on Unix; `os/file_unix.go` passes `O_CLOEXEC`, and a test on Linux confirmed it), so a child process cannot
   keep the lock alive.
5. **Acquire**, in this order, for item `exec:<id>` and owner `run:<uuid>`:
   1. Read the lease row. If a row exists and its `machine` differs from this machine's identity, or is empty (a row
      written before the migration, or by the legacy `AcquireLease`, which never writes it), **refuse**: the holder
      cannot be judged. The message names the owner, host, pid
      and acquired time, and the manual command (see Escape hatch). The host name is printed, never compared.
   2. Try the lock without blocking. If it is held, **refuse** with the same message: the holder is alive.
   3. Holding the lock: in one transaction, **read the row again** (a legacy `AcquireLease` may have inserted one
      since step 1) and apply step 1 to it: a row with an empty or foreign `machine` is refused, never taken over, and
      the lock is released on that path. If no row exists, insert it (token as today, from `lease_history`); if a row of
      this machine exists, it is a crashed run's: replace owner, machine, host, pid and acquired time and **bump the
      token by one** only if the row still has the owner and token read inside the transaction (a compare-and-set),
      recording the new token in `lease_history`. The caller is not told whether this was a takeover: recovery does not
      need it, since `RecoverCrashedSession` detects a crash from the log on every run (`crashedFromEvents`), after a
      takeover and after an `unlock --force` alike.
   4. Return a handle holding the lock, the item, the owner and the token.
6. **Release** deletes the row by owner and token, as `ReleaseLease` does, then closes the lock. A killed process
   leaves the row and loses the lock.
7. **Order with recovery.** `RecoverCrashedSession` runs after the acquire (step 5) and only when ownership was
   established. It is never run by a process that was refused. Today `cmdRun` and `serveRun` run recovery, then the
   resume of a paused session and the steering drain, and only then does the loop take the lease (`RunExecLoop` in
   `execloop.go`). The acquire moves to just after the session is loaded and found not terminal (ADR-002 phase 1, which
   rejects terminal sessions first and stays first) and before phase 2; the status is read again once the lease is held,
   because it may have changed in between. The loop receives the held handle instead of taking its own. The preamble is
   shared by the loop and by the text-only turn (`runSessionTurn` chooses between them afterwards), so the acquire at
   the wiring packet covers text-only runs too, and the handle is held until `runSessionTurn` returns. This is also
   needed because delegate resume claims the lease and the loop then re-grants it to the same owner
   (`delegateresume.go`; its documentation says so): with a lock file the second acquire by the same process would
   conflict with its own lock, so a held handle replaces the re-grant.
8. **The handle outlives the post-loop writes.** `RunExecLoop` releases its lease with a deferred `ReleaseLease`, and
   writes follow the return: in `execloop_session.go`, `RunSessionExecLoop` calls `finishExecLoopState` (which calls
   `pauseExecLoopRun`) and `completeExecLoopRun`; the swarm coordinator in `swarmexecute.go` calls the same two; the
   delegate run's settle in `delegate.go` calls `cancelSwarmChild` (defined in `swarmexec.go`), `failDelegatedChild` and
   `completeExecLoopRun`. `finishExecLoopState` also calls `CancelSession` on a cancelled run, which appends
   `session.cancelled`, and `session run` on the text-only path writes through `TurnWithOptions` the same way. Those
   append events and change the status of a session after its lease is gone, so the new owner of the run keeps the
   handle until they return; the loop does not release it. No line numbers are cited anywhere in this ADR, because they
   move.
9. **Children.** The lease of a delegated child uses the same acquire. When a crashed parent is recovered, the
   parent's lock is already held by the recoverer, and each child's lock is assumed free (its runner is presumed gone;
   this is not checked, see known limit L1), so `ClearLeaseOwnedBy` keeps its role, run only after ownership.

10. **Legacy rows and ordering.** `AcquireRunLease` is the only writer of `machine`, and the only acquire that takes a
    lock file. The legacy `AcquireLease` keeps its owner-based re-grant and writes `machine = ''`, no lock and no host
    or pid. A row it wrote is therefore unjudgeable: `AcquireRunLease` refuses it, live or crashed, and never takes it
    over, however the lock reads (it reads "free", since the legacy holder took none). The reverse is already so: the
    legacy acquire refuses any row of another owner. This is what makes the order of the packets safe: no packet that
    switches `session run` to the judging acquire needs the coordinator, delegate children, resume, compact, text-only
    turns or `tool run` to hold their leases through the new acquire first, because until they do their rows read
    "unjudgeable, refused" rather than "dead, taken". Without this rule, a live `delegate:<uuid>` child or
    `compact:<uuid>` row, written by the legacy acquire on this machine and holding no lock, would be judged dead and
    taken over: the false "dead" this ADR exists to prevent. The interim cost is the one D9 accepts for old rows: a
    legacy row left by a crash needs `session unlock --force` once `session run` has switched (a crashed legacy
    `compact:` row was cleared by the next `session compact` until packet 12, which removed that clearing).
    `README.md` said that such a row "is cleared by the next `session run` or `session compact`"; that stops being
    true when `session run` switches, because `session run` then refuses it (empty `machine`) and only
    `unlock --force` clears it (after packet 12, neither `session compact` nor `RecoverCrashedSession` does). The
    same sentence stands in `docs/architecture/OVERVIEW.md` (the paragraph on `session compact`). The wiring packet
    therefore rewrites both, and the packet that moves compact rewrites both again (the whole passage, which ends "a
    `session run` or a second compaction started during a compaction takes its lease, and the one in flight fails
    fenced": the `session run` half is false from the wiring packet, the second-compaction half from the wiring packet
    until the compact packet); each packet carries its own document edits, so no document is false between merges.

### Judgement table

| Row | Machine | Lock | Result |
|---|---|---|---|
| none | | free | Grant; token from history |
| none | | held | Refuse (another process is mid-acquire or alive) |
| present | this machine | held | Refuse: live holder named |
| present | this machine | free | Take over, token + 1, then recover |
| present | other machine, empty (legacy row) or unknown | any | Refuse: cannot be judged; name the holder and the unlock command |

### Failure modes, spelled out

- **Different machine, shared database on a network drive.** Never taken over. SQLite's own documentation, as I
  recall it (not verified here), says WAL mode does not work over a network filesystem, and the store opens the
  database with WAL (`store.go`), so a networked database is already outside what SQLite supports. Two machines
  with the same machine id (cloned images that kept `/etc/machine-id`) are the residual risk of the identity check;
  accepted and documented.
- **PID reuse and clock skew.** Not inputs. The pid is displayed, never compared with the process table.
- **A suspended laptop or a stalled provider call.** The process is alive and holds the lock, so it is never judged
  dead; there is no TTL to expire.
- **Containers.** Containers on one kernel that share the database and lock directory through a volume see the same
  lock and are judged correctly; a pid from another namespace is never looked up. The machine identity decides
  whether a crashed row can be taken over:
  - containers that share the host's `/etc/machine-id` (bind-mounted) match, and a crash is taken over;
  - an image that bakes in one `/etc/machine-id` gives every container the same identity: they match each other,
    and a row from a different machine running the same image would also match (the cloned-image risk above);
  - a minimal container with no `/etc/machine-id` falls back to `host:<host name>`, and `docker run` gives a fresh
    host name every time, so a crash row from an earlier container is refused until `unlock --force`; setting
    `--hostname` or mounting a machine id removes that;
  - the safe direction holds in every case: an unjudgeable row is refused, never taken.
- **WAL and multiple processes.** The lease and the log are in one database, so WAL's multi-process readers and one
  writer apply as they do today. The lock file does not replace a database transaction.
- **Locks and the filesystem.** `flock` on a network filesystem is not reliable. Where the lock directory is on one,
  the judgement is only as good as the filesystem's lock service, so the lock directory must be on the same local
  disk as the database; a database on a network drive is unsupported (above). On Linux NFS, `flock` is emulated with
  POSIX byte-range locks, which belong to a process rather than to an open file description (inferred from kernel
  behaviour, not tested), so the "second handle in one process conflicts" spec would not hold there; the specs run on
  a local filesystem.
- **An inherited descriptor.** A child process that outlives the run and inherited the descriptor would keep the
  lock, so a dead run looks alive: the safe direction, and it ends with the child. Close-on-exec removes the
  common case; a spec pins it.
- **A deleted lock file.** Removing the file while a run holds it lets a second run create a new one and lock it: two
  writers. FlowState never removes it, and the packet's acquire compares the open file with the path after locking on
  Unix and retries when they differ. On Windows an open file cannot be deleted: `syscall_windows.go` in the standard
  library opens files with share mode `READ|WRITE` only, so a delete is refused (a source reading, not a run).
- **A lock file that cannot be created** (read-only directory) refuses the run with an error naming the path; the
  database in that directory could not be written either.

### Takeover, recovery and the other writers

- **When the holder is alive**, `session run` refuses with a new error, `ErrSessionBusy`, naming the holder. It is
  not `ErrExecLoopFenced`, which also covers the stale-token case. Today a held lease reaches the user as code
  `internal`, exit 1 (the default arm of `exitCodeFor` in `cli.go`) and HTTP 500. **Decided (D11):** a new code
  `session_busy` (`internal/app/errors.go`), HTTP 409 like `session_terminal` (`internal/server/errors.go`), and a
  new exit number 9 (the constants at the top of `cli.go` use 0, 1, 2, 4 to 8 and 130; 3 is the provider error,
  `exitProviderError` in `sessionrun.go` and the README exit-code table; I grepped `cmd/` and no constant or return
  uses 9, so 9 is the next free number), so a script can tell "retry later" from `internal`. `schemas/cli.json` has no
  error-code enumeration (it has only `"error": {"type": "string"}` fields; I searched it), so it does not
  change; the README and `docs/architecture/OVERVIEW.md` exit and code text do. The owner may still change the
  number; nothing else depends on it. **This amends an external contract.** `internal/server/errors.go` says the code
  set is closed per `specs/CLI-AND-API-CONTRACT.md` section 2, a file that is not in this repository (I could not
  read it), and the exit numbers in `cli.go` are a stable contract. Adding `session_busy` and exit 9 therefore needs
  that specification amended first; that amendment is a prerequisite of the error-contract packet and open question
  1.
- **`session compact` reuses the rule.** With `compact:<uuid>` as its owner it takes the lease by the same acquire:
  refused on a live holder, and on a crashed one it takes over and runs recovery first, as `session run` does. That
  removes the "resume with `session run` before compacting" limit of the conservative patch, and the interim
  `compact:` row clearing of #458 (`clearCompactLeases`, called from `CompactSession` and `RecoverCrashedSession`),
  which packet 12 deleted. #458 is merged, so this builds on `CompactSession` as it is.
- **Writers that take the lease.** `session run` (text turn and loop), a serve run, the swarm coordinator, compact,
  delegate runs (already), and `tool run`, which appends `tool.call` and `tool.result` events with an event writer
  and no lease today. The resume of a paused session and the steering drain are part of the run's preamble, so they
  run under it, and so do the post-loop writes of rule 8. The text-only turn is a writer and is no longer exempt.
- **Writers that are covered another way.** A fresh or under-parent writer needs no lease of its own, because no
  other process can hold the session or its parent's lease already. Read from the code:
  - fresh session, no contender: `openChildSession` (`internal/app/sessioncmd.go`, writes `session.delegated` and
    the first turn of a child it has just created) and `Composition.SessionLoop` (`composition.go`; no non-test
    caller);
  - under the parent loop's lease: `appendParentReceipt` (`delegate.go`, delegation events in the parent's log);
  - under the child's claimed lease, once the resume has claimed it: `markLostTurn` and `prepareResumedChild`
    (`delegateresume.go`, two writers);
  - `cancelSwarmChild`, when `cancelSwarmChildren` calls it (`swarmexec.go` in the cancel and abort paths of
    `RunSwarm`, `swarmexecute.go` in the coordinator): one `session.cancelled` on a child whose run has ended or never
    started; the coordinator's call moves under its handle with the swarm packet;
  - `RunSwarm` (through `RunSwarmFile`, called by `swarmcmds.go` and `serve.go`) takes an existing parent session and
    creates fresh children. A search of `swarmexec.go` finds no event writer on the parent; I did not trace
    `attributeSwarmChild`, so whether it writes the parent's log is open and belongs to the swarm packet.
  - outside `internal/app`: `internal/session/storeext.go` (`Create` at its start and `Close`, which `session close`,
    `session cancel` and `CancelSession` use: lease-free by decision, below), `internal/store/messages.go` (the
    steering drain: under the run's lease) and `internal/tool/loop.go` (appends through a writer its caller passes in:
    `tool run` and the exec loop, so it takes the lease of its caller).
  A grep of `cmd` and `internal` for every `NewEventWriter` call and every event append finds no other writer.
- **Writers that do not.** `session message` only enqueues a row (`SendMessage` takes no event writer) and the drain
  moves it into the log under the run's lease. `session close` and `session cancel` open an event writer and no
  lease today; they stay lease-free, because ending a session whose holder is dead or stuck must not depend on the
  lease. A close racing a live run in another process is the same residual as the one this ADR closes for `run`; it
  is out of scope and noted in Consequences.
- **Read-only commands** (`session doctor`, `session replay`, `session events`, `session list`, the server's reads)
  never take the lease. No non-test code under `cmd/flowstate` takes a lease today; they stay that way.
- **HTTP.** `serveRun` is the same preamble as `cmdRun`, so it takes `run:<uuid>` like the CLI does. Two
  `flowstate serve` processes, or a serve and a CLI, on one database contend for the same lock file and the second is
  refused. Two concurrent runs of one session inside one serve process are refused by the same lock: a second open
  file description conflicts even within a process (run on Linux with `syscall.Flock`; Windows is
  recalled, and NFS differs, see above; a spec pins it).

### Escape hatch

**`flowstate session unlock <id>` is needed now**, because the refusal of a lease that cannot be judged (another
machine, or a row from before the migration) has no other way out, and it ships before the refusals do (packets 5 and
6). Without `--force` it is read-only: it prints the holder and what `--force` would do, and exits non-zero. With
`--force` it deletes the row only if it is still the one read (owner and token: a new store method, since
`ClearLeaseOwnedBy` deletes only if the owner and token still match the row it read, and takes no expected token
from its caller); the next `session run` then acquires, recovers and runs.
`--force` refuses when the lock is held on this machine: a holder that holds a lock is never overridden; stop that
process instead. That covers only rows written by `AcquireRunLease`. A live legacy row (`delegate:<uuid>`,
`compact:<uuid>`, the coordinator's `cli`, or any row from before the migration) holds no lock, so the check cannot see
it, and `--force` deletes it while its run continues: two writers again, by explicit operator action. The command says
so: `--force` prints a warning that it cannot tell whether a row it did not lock is still running, and the operator must
check first. Until the legacy writers have moved (see the packets), that is the price of an escape hatch that must work
for rows nothing can judge. It runs no recovery: recovery is log-driven and the next run does it. It does not replace
the lock for judgeable rows: a row of this machine with a free lock is taken over by `session run` itself, so `unlock`
is not needed after an ordinary crash on a machine whose identity is stable.

**Audit.** `unlock --force` deletes a lease row, possibly of a live run on another machine, so it leaves a trail: one
engine event `session.unlocked` with `{owner, host, pid, forced}` in the session's log, and one line on stderr with
the same fields. Nothing more (no new table). The event is a new type; there is no event catalogue file (types are
constants in `*_types.go` and literals), so the packet adds a constant and a spec.
`Replay`, `session doctor` and `session events` were run on a seeded `session.unlocked` event and accept it, as do
`rebuildHistory` and `crashedFromEvents` (which skips unknown types); I have not rerun that since. **One consumer
rejects it: `LoadSessionHistory`** (`internal/app/history.go`) fails closed with `ErrInvalidHistory` on a type it
does not know (the `default` arm of its event switch). It has no non-test caller in this tree (grep), and it already
rejects `session.interrupted`, which is also absent from its allow-list. That is kept, not changed, and the unlock
packet pins it with a spec so nobody is surprised. If the session is terminal the event is skipped and the stderr line
remains.

### Compatibility

- **Old rows.** A row from before the migration has owner `cli` (or `delegate`) and no machine (the column default is
  `''`); so does any row the legacy `AcquireLease` writes while it still has callers. Unique owners cannot match it,
  so it is not re-granted; the judgement table treats an empty machine as unknown and refuses, naming
  `session unlock`. A crashed session from before the upgrade therefore needs one `unlock --force`. The alternative,
  judging such a row by the lock alone, would also be right after a crash and wrong during an upgrade while an old
  binary is still running a session (it holds no lock file). The refusal costs one command; the other costs two
  writers.
- **An old binary mid-run during the upgrade** is not excluded: the barrier is checked when a database is opened, and
  a running binary has already opened it. Do not upgrade while a run is in progress; the documentation says so.
- **One new event type, `session.unlocked`, written only by `unlock --force`.** No existing payload changes and
  replay is untouched; `lease_history` keeps its meaning. `LoadSessionHistory` rejects the new type (see Audit).
- **No new status.** The lease is a table row and a file; the session's status machine (ADR-002) is unchanged.

### Decisions

| # | Decision |
|---|---|
| D1 | Every writer of a session log holds the exec lease `exec:<id>` under an owner unique to the run: `run:<uuid>`, `compact:<uuid>`, `delegate:<run id>`. The constant `cli` is retired. |
| D2 | The lease row gains `machine` (the hex HMAC-SHA256 of the machine id, never the raw id), `host` (display only) and `pid` (schema v10), written only by `AcquireRunLease` (the legacy `AcquireLease` leaves `machine = ''`), declared `not null default ''` / `default 0` so AutoMigrate can add them. No start time, no heartbeat column. v10 is a downgrade barrier (`ErrFutureSchema` in older binaries) and `<db>.pre-v10.bak` is taken before the migration. |
| D3 | Liveness is an exclusive non-blocking advisory lock (`flock` / `LockFileEx`) on a per-item lock file, held for the whole run and released by the kernel on process death. The lock key is the database file's identity (resolved path, device and inode, or the Windows file index), not the `--db` string; the database file itself is not locked. A path-less store uses a process-local table. |
| D4 | A lease is taken over only for a row whose stable machine identity matches this machine and whose lock is free: compare-and-set on owner and token, token bumped by one, `lease_history` updated. A row of another machine, or with no machine (every row the legacy `AcquireLease` wrote), is never taken over. The host name is never compared. |
| D5 | A refusal names the owner, host, pid and acquired time, and the manual command. Refusal is the answer to every case that cannot be judged. |
| D6 | `RecoverCrashedSession`, the paused-session resume and the steering drain run only after ownership is established, under the lease. The loop receives the held handle and does not re-acquire, and the handle is held until the post-loop writes have returned. Packets may merge in the order below because a legacy-held row is refused, not judged (rule 10). |
| D7 | `session compact`, `tool run` and the text-only `session run` take the same lease; `session message` (enqueue), `session close`, `session cancel` and the read commands do not. |
| D8 | `flowstate session unlock <id> [--force]`: without `--force` it reports; with it, it deletes an unjudgeable row and refuses a lease whose lock is held on this machine. `--force` leaves a `session.unlocked` event and a stderr line. |
| D9 | Old `cli` and `delegate` rows, and rows of the legacy acquire, are treated as unjudgeable and refused until `unlock --force`. Decided; the open question below is only about the alternative. |
| D10 | Not now: leases across machines, TTL and tuning knobs, a heartbeat, a daemon. |
| D11 | A refusal is code `session_busy`, HTTP 409, exit 9. |

## Consequences

- A second `session run`, `compact`, `tool run` or serve run on a live session is refused before anything is
  written, and recovery can no longer answer a live run's calls. The documented limit in `RecoverCrashedSession` and
  ADR-009 D8 is removed in the packet that implements this. ADR-009 D8 and the comment in
  `internal/app/session_recovery.go` say recovery runs "before the lease and the first provider request": that turns
  false at the wiring packet, which moves recovery after the lease, so the wiring packet rewrites both.
- The sentence about a crashed compaction's lease row (`README.md` and `docs/architecture/OVERVIEW.md`) would be stale
  from the wiring packet until the compact packet (rule 10); both packets rewrite both documents in the same change.
- A crashed run is taken over by the next run with no manual step, on the same machine. A crashed run on another
  machine, one left by an older binary, and one whose machine identity changed (a container with no
  `/etc/machine-id` and a fresh host name per run, a reinstalled OS, a restored `/etc/machine-id`) need
  `session unlock --force`. With a readable stable machine id that is rare; with the host-name fallback it is every
  crash in a throw-away container. Open question 3 asks whether that is acceptable.
- A lock file directory appears beside the database, fanned out by hash prefix. FlowState does not clean it now; a
  stale file is harmless because the lock, not the file, is the signal, and a prune is feasible later. Backing up or
  copying a database does not need it. A residual remains: a DB-only bind mount seen from another mount namespace
  can give a second lock directory and so a false "dead" for a live run.
- The database holds a keyed hash of the machine id, not the id, so a backup or a bug report does not leak it.
  Anyone who already has the machine id can recompute the hash; that is what a keyed hash of a stable id is.
- A path-less (in-memory) store is excluded within one process only.
- A new package owns the OS primitive, with platform-specific files and `golang.org/x/sys/windows` as a direct
  dependency; the windows and darwin vets cover it.
- A refusal is a new class of failure for scripts that rely on running two `session run` commands against one
  session. That was two writers; the refusal is the intent.
- Fencing is still checked once per turn and once before the compaction append. The takeover rule does not rely on
  it (the holder it replaces is gone), but a design that ever judged a live holder dead would have to fence appends
  in their own transaction first. Not done here.
- **Known limit L1: parent recovery clears unfinished children's `delegate:` rows without checking their lock.**
  `ClearLeaseOwnedBy` deletes by owner prefix and token only, so recovering a parent can delete the judged row of a
  live resumed child and let it be fenced. A later change should delete only legacy rows, or check the lock first.
- A `session close` or `session cancel` in another process while a run is live is still two writers of the log. It is
  the next gap after this one.
- **Open, unchecked: the cancel race.** `session cancel`, run against a session with two live runs
  (the status quo): it exited 0 and appended `session.cancelled` at seq 4 while both runs were still alive, and no
  failure was visible then; what the live runs wrote afterwards was not observed. Whether a live run, now sole and
  leased, notices an external cancel and stops cleanly is not established; `cancel` stays lease-free (D7).
- Left unchecked: the `--task` branch of `session run` (`runSelfTestTask`), which creates its own session before it
  runs; I did not trace it.

### Not now

Distributed leases across machines; lease TTL and tuning knobs; a daemon that owns sessions; a heartbeat (open question
2); recording the previous holder in the `session.interrupted` payload (a useful audit field, a payload change for
its own packet); closing the `close`/`cancel` race.

### Packets

Packets are numbered 1 to 13 (the liveness options above use the letters (a) to (d)). Each lands with the specs that
name its negative oracle: the spec that must fail when the change is removed, and each is meant to be one PR within the
budget (600 changed lines and 10 paths for a non-feature, `FLOWSTATE_PR_MAX_LINES`; **spec files count as paths**, and
so does every file a new command or error code touches, and every document that must change with it). Sizes and path
counts are estimates, and every packet is "at most 10 paths; the exact list is in the packet PR, found by grepping the
callers of the changed API"; the paths named here are the production files and the specs known to be touched, not a
count the ADR promises. They were made by listing the files each packet names and, for packets 1 and 2, by grepping the
tree for `foreignKeyVersion`, `.pre-v9.bak` and the literal 9 (the first count of 7 paths omitted the tests and
`errors.go` that hard-code them); a packet that nears a limit is split again, not padded. The schema work is two packets
for that reason: packet 1 changes the version and the backup (9 paths, because every assertion on 9 follows), packet 2
adds the columns (3). The order matters, and it is safe for the reason in rule 10: `AcquireRunLease` is the only writer
of `machine`, so every row of a not-yet-moved writer reads "unjudgeable, refused". `unlock` precedes the refusals that
name it; unique owners without takeover would make every crashed session unrunnable (the old `cli` re-grant is what
reclaims it today), so the loop keeps taking its own lease, under the old owner, until packet 13 has passed every caller
a handle and removed the fallback with its specs. **No packet switches `session run` to the judging acquire (packet 9)
before packets 1 to 8 have merged; packets 10 and 12 may follow in either order, and packet 13 follows both.**

| Packet | Content | Size, paths | Negative oracle |
|---|---|---|---|
| 1 | The schema version and the backup, with no column: `leaseLivenessVersion = int64(10)` beside `foreignKeyVersion` (which stays 9), `knownSchemaVersion` raised to it, version 10 recorded, and the backup generalised (`.pre-v9.bak` as today; `.pre-v10.bak` for a database at v9 or at keyed v8). Every existing assertion on 9 follows, and the two `DELETE ... WHERE version = 9` statements in `fkmigrate_test.go` (the v8-declares-every-key spec and the `backupBeforeRebuild` spec) become `version >= 9`: at v10 `= 9` leaves max 10, so the first would pass vacuously and the second would see `ErrMigratedElsewhere` instead of `ErrBackupExists`. Nothing else changes; the legacy `AcquireLease` is untouched. | about 200 lines; 9 paths: `migrate.go`, `fkmigrate_types.go`, `fkrebuild.go`, `errors.go` and the specs `migrate_test.go`, `fkmigrate_test.go`, `info_test.go`, `internal/app/storeinfo_test.go`, `cmd/flowstate/storeinfo_test.go` | A database written at v10 is refused by a build that knows 9 (`ErrFutureSchema`); a v9 database opens at 10 and has `.pre-v10.bak`, and removing the backup call leaves none; a v8 database still gets `.pre-v9.bak` only; a v10 database gets no backup; the version specs expect 10 (`knownSchemaVersion` left at 9 fails them). |
| 2 | The columns: `machine`, `host` and `pid` on `leaseRow` with `not null default` values. Nothing writes or reads them; the legacy `AcquireLease` is unchanged and its rows keep `machine = ''`. | about 120 lines; 3 paths: `orm_types.go`, `lease_test.go`, `migrate_test.go` | A v9 database gains the columns and its rows survive (an `ADD COLUMN ... not null` without a default fails the spec); a row written by `AcquireLease` reads back `machine = ''` (a legacy acquire that records a machine fails it). |
| 3 | The machine identity: the reads for Linux, macOS and Windows, the HMAC-SHA256 (key = the id, message `flowstate-lease-v1`), the `host:<name>` fallback, the `golang.org/x/sys` `go.mod` edit. No caller. | about 350 lines; a new package of a shared file, one file per OS (3), a types file and specs (1 or 2), `go.mod`, `go.sum`: 9 to 10 | **The raw id never appears in the value** (a spec reads the value against a fake id, and fails if the id, or any substring of it, is present; returning the id makes it fail); two machines with different ids differ; the same id gives the same value; each read is pinned with a fake file or registry on its own platform; no readable id gives `host:<name>`. |
| 4 | The lock primitive: identity (resolved path, device and inode, Windows file index), the fanned-out lock files, acquire, try-lock, release, close-on-exec, the Unix and Windows files, the descriptor-versus-path check, the process-local table for path-less stores. | about 450 lines; a lock package of shared, Unix and Windows files for the lock and for the identity (6), the table, a types file and their specs (2 to 3): 10 at most, so the path-less table splits off if it goes over | Two acquires of one item conflict, in one process and from a child process; **the same database opened through a symlink, and by `./f.db` and an absolute path, conflicts** (without the identity, both are "free"); a child killed with SIGKILL leaves the lock free (Unix only); a child that inherited the descriptor does not keep it; an in-memory store excludes within one process. Returning "free" always makes the conflict specs fail. |
| 5 | The store and app side of `unlock`: a store method that reads a lease row (owner, token, machine, host, pid), a store method that deletes a lease only if owner and token still match, the `session.unlocked` constant, an app function that reports or (forced) deletes, refuses a lock held on this machine and appends the event, and a spec in `history_test.go` that `LoadSessionHistory` rejects `session.unlocked` with `ErrInvalidHistory`. It needs the columns of packet 2, the machine identity of packet 3 and the lock of packet 4. | about 400 lines; `lease.go`, `leaseclear.go`, a store lease types file, an app `unlock.go` and its types file, and the specs `lease_test.go`, `leaseclear_test.go`, `unlock_test.go`, `history_test.go`: 9 | A delete with a changed token or owner removes nothing; without force nothing is deleted; forced, a row of another machine is deleted and the event is appended; a lease whose lock is held on this machine survives; `LoadSessionHistory` rejects the new type (the spec pins today's behaviour). |
| 6 | The command `session unlock <id> [--force]`: argument parsing, the stderr line, the report output, `dispatch.go` and its usage text, the command types, `schemas/cli.json` (`cli_test.go` validates each command's output against it), a README command row. | about 300 lines; `sessionunlock.go`, `sessionunlock_test.go`, `dispatch.go`, `types.go`, `schemas/cli.json`, `README.md`: 6 (#458 touched 14 paths for a command that also changed the engine) | Without `--force` the command prints the holder, deletes nothing and exits non-zero; with it, the row of another machine is gone and the stderr line is written; its JSON output validates against the schema (a missing schema entry fails the existing schema spec). |
| 7 | Store-level `AcquireRunLease` with the judgement table, the compare-and-set takeover, the handle that records `machine`, `host` and `pid`, and `ErrSessionBusy` with the holder in its message. No callers. | about 400 lines; `runlease.go`, a store lease types file, `lease.go` (shared token history), `runlease_test.go`, `lease_test.go`: 5 | A row of this machine with a held lock is refused and the log is unchanged; a row of this machine with a free lock is taken over with token + 1 and `lease_history` records it; a row of another machine, or with an empty machine (a row of the legacy `AcquireLease`, whatever its owner and with the lock free), is refused and never taken over (a judge that treats an empty machine as this machine fails it); a lost compare-and-set refuses. |
| 8 | The loop takes a held handle: `ExecLoopConfig` and `SessionExecLoopRequest` (in `execloop_types.go`) gain an optional handle, and `RunSessionExecLoop` forwards it; with one, the loop does not acquire or release, and the handle stays held until `finishExecLoopState`, `pauseExecLoopRun` and `completeExecLoopRun` have returned (rule 8). With none, today's acquire stays. With one, the handle supplies the item, the owner and the token, so the loop's two `FenceLease` calls (`acquireExecLease`, `beginExecRequest`) use the handle's, not `LeaseAgent` and the token from the config. | about 300 lines; `execloop.go`, `execloop_types.go`, `execloop_session.go` and `execloop_test.go`, `execloop_session_test.go`: 5 | With a handle, the loop does not re-acquire (a second acquire by the same process would conflict, and the spec fails if the loop tries); releasing the handle before `completeExecLoopRun` lets a second run in, and a spec fails; a handle given to `RunSessionExecLoop` reaches the loop (a forward that drops it fails the spec). |
| 9 | `session run` and `serve` switch to the unique `run:<uuid>` owner and the acquire-then-recover order (rule 7): acquire after phase 1, status read again, `RecoverCrashedSession`, resume and drain under the handle, then the loop with the handle (through `RunSessionExecLoop`, which packet 8 already made forward it). **The acquire sits in the shared preamble, so it covers the text-only turn as well**, and the handle is held until `runSessionTurn` returns. The other writers still use the legacy acquire. Documents, in the same packet: the whole passage on a crashed compaction's lease row in `README.md` and in `docs/architecture/OVERVIEW.md`, up to "the one in flight fails fenced" (rule 10: `session run` now refuses a `compact:` row; its second-compaction half is false again until packet 12); the ADR-009 D8 sentence "before the lease and the first provider request" and the same comment in `session_recovery.go`, both false once recovery runs after the lease; and the ADR-002 "Phase order" block, which gains the lease step between phase 1 and phase 2. | about 400 lines; 10 paths: `sessionrun.go`, `serverun.go`, `session_recovery.go`, the specs `sessionrun_test.go`, `serverun_test.go`, `session_recovery_test.go`, `README.md`, `docs/architecture/OVERVIEW.md`, `ADR-002-session-run-lifecycle.md`, `ADR-009-delegation.md` (the type field and the forward moved to packet 8; if the grep of callers goes over 10, the packet splits by code, never by moving its documents out) | A live row refused by a second run leaves the log byte-identical; a crashed row is taken over and recovery then appends `session.interrupted`; **A second text-only `session run` against a live first one is refused and the log is unchanged** (a preamble that does not take the lease fails it); reverting to the constant owner makes the first fail. **Legacy-held rows (rule 10): a live `delegate:<uuid>` row and a live `compact:<uuid>` row, written by the legacy acquire with this machine as the process and no lock, are refused by `session run`, not taken over, and the log is unchanged** (a judge that reads an empty machine as this machine, or a legacy acquire that records one, fails it). |
| 10 | The swarm coordinator and the delegate and delegate-resume runs pass their own handles (the `run:` owner for the coordinator, `delegate:<run id>` for children; resume passes its claimed handle instead of the re-grant), keep them through their post-loop writes. The loop's own-acquire fallback stays until packet 13. | about 350 lines; `swarmexecute.go`, `delegate.go`, `delegateresume.go` and `swarmexecute_test.go`, `delegate_test.go`, `delegateresume_test.go`: 6, plus any caller the grep finds (at most 10 paths) | A delegate resume under a held handle runs (it would conflict with its own lock if the loop re-acquired); the coordinator's post-loop `completeExecLoopRun` runs under the handle (releasing early lets a second run in); a second `session run` against a live delegate child is refused. |
| 11 | The error contract (D11): `session_busy`, HTTP 409, exit 9, README and OVERVIEW text. **Prerequisite: the owner amends `specs/CLI-AND-API-CONTRACT.md` section 2** (the closed code set, outside this repository; open question 1) or accepts the amendment with this ADR; the packet does not start without it. A real two-process spec: a second `session run` against a live first one exits 9 and writes nothing. | about 350 lines; `errors.go` in `app` and in `server`, `errors_test.go` in `server`, `cli.go`, `cli_test.go`, `sessionrun_test.go`, README, OVERVIEW: 8 (`schemas/cli.json` has no error-code set and stays out) | The two-process spec fails when the code maps to `internal`; the server spec expects 409 `session_busy`; `cli_test.go` pins exit 9. |
| 12 | The lease around `tool run`; `session compact` moves to the rule and `clearCompactLeases` goes; the documented limit in `RecoverCrashedSession` and ADR-009 D8 is lifted (the "before the lease" sentence moved to packet 9), and the sentence on a crashed compaction's lease row is rewritten again in `README.md` and `docs/architecture/OVERVIEW.md` (it is true once more: `session run` takes the row over). The text-only turn is not here: packet 9 covers it. | about 400 lines; 9 paths: `internal/app/toolrun.go` (`RunSessionTool` appends the events and already takes `dbPath`; `cmd/flowstate/toolcmds.go` only calls `app.RunSessionToolFromPath`, so no lock code goes in the adapter, ADR-007), a new `internal/app/toolrun_test.go` (the spec at app level; `cmd/flowstate/toolcli_test.go` has no implementation pair and stays unchanged), `compact.go`, `compact_test.go`, `session_recovery.go`, `session_recovery_test.go`, ADR-009, `README.md`, `docs/architecture/OVERVIEW.md`; if it grows past 10, `tool run` splits off with its own documents | `tool run` on a live session is refused and the log is byte-identical (fails when the lease is not taken); compact on a live session leaves the log byte-identical; compact on a crashed one takes over and compacts; a crashed `compact:` row left by the legacy acquire is no longer cleared silently, and the spec says what happens instead. |
| 13 | The fallback is removed and the specs are migrated, in one packet because the removal breaks every direct caller of `RunExecLoop` and `RunSessionExecLoop`: with no handle the loop errors, so each spec acquires one. One shared test helper (`acquireTestLease`, taking the store, item and owner and returning the handle and a cleanup) lives in `execloop_test.go`, which pairs `execloop.go` (a new helper file would have no implementation pair), and each other spec changes by one line per call. | about 300 lines; at the base the callers are `execloop_test.go` (9 calls), `execloop_session_test.go` (11), `compacttrigger_test.go` (6), `delegate_test.go` (3), `delegatetool_test.go` (2), `delegatenest_test.go` (1), `delegateparallel_test.go` (1) and `delegateresume_test.go` (1), plus `execloop.go`: 9 paths, and `swarmexecute_test.go` if the grep finds calls there; if the grep finds more than 10, split by file (the `delegate*` specs, then the rest) with the helper in the first | The loop given no handle returns an error and takes no lease (a retained fallback fails it); every migrated spec still passes with the handle held for its whole run; a second `session run` against a handle-held row is refused. |

## Alternatives rejected

- **Pid and process start time (a).** Per-OS process-table code for three platforms, and a pid from another namespace
  reads as dead, which is the harmful direction.
- **A heartbeat with a TTL (b).** Needs a clock, a TTL that is wrong for a long provider call or a suspended machine,
  and fenced appends that do not exist yet. ADR-006 rejected heartbeats for crash detection too, for the write path
  and the interval to tune.
- **Leaving it (d).** The live-run bug above, kept and documented.
- **One shared owner per kind of command.** The `cli` of today; its re-grant is the bug.
- **Comparing host names.** Unstable (network, containers, renames), so it would make `unlock --force` routine; the
  host name is display only and a stable machine identity decides.
- **Keying the lock by the `--db` string.** Two spellings of one file get two "free" locks and a live run is taken
  over; the key is the file's identity.
- **Locking the database file itself.** Collides with SQLite's own locking (rule 2).
- **Taking over a row of another machine when its lock is free.** A lock file on a local disk says nothing about a
  process on another machine.
- **Judging a pre-migration row by the lock alone.** Cheaper for the operator and wrong when an old binary is running.
- **A database-held lock (a long transaction or table lock).** Ties a run to one connection and to SQLite's busy
  timeout, and a crashed process's lock is released by SQLite's own recovery on a schedule this ADR cannot state.
  Not investigated further.

## Open questions for the owner

1. **The exit number.** D11 chooses `session_busy`, HTTP 409 and exit 9. The exits are a stable contract (`cli.go`);
   the owner may prefer another number (3 is the provider error, so 9 is the next free one). Nothing else depends
   on it. Adding the code also amends `specs/CLI-AND-API-CONTRACT.md` section 2 (outside this repository, which closes
   the code set); the owner must amend it first, or accept the amendment with this ADR.
2. **A heartbeat.** The lock file answers "alive" without a clock. Does the owner want a last-seen time in the row
   for display (`session doctor` could show "held by pid 123, 40 seconds ago")? It would be a column and a periodic
   write between turns, for display only.
3. **Same machine only.** Is it acceptable that a database on a network drive or shared between machines is
   supported only by refusal and `unlock --force`, and that a throw-away container without a machine id (host-name
   fallback) needs `unlock --force` after a crash? The alternatives are a lease service, which is a different
   product (not now), or a rule for such containers (for example refusing to run without a stable identity).
4. **The UX alternative for a pre-migration row.** D9 refuses it, which costs one `unlock --force` per crashed
   session after the upgrade. The owner may prefer judging it by the lock alone and accepting the upgrade-window
   risk (an old binary still running holds no lock file). Decided as D9 unless the owner overrules it.
5. **`close` and `cancel`.** Should they take the lease and refuse a live run (a cancel could then not stop a run
   in another process), or signal the holder? Left as is; see also the cancel race in Consequences.
6. **Where the lock directory lives.** Beside the resolved database file, as `<resolved path>.locks/`, is decided
   (rule 1). The alternative, a per-user state directory, would survive a read-only database directory but would
   give two users, or a container and its host, two lock directories for one database, which is the false "dead" this
   ADR avoids. Does the owner accept a read-only database directory being refused?

## Cross-links

- ADR-002 (session run lifecycle): phase 1 (load, reject terminal) stays first; the lease is acquired right after it and
  ahead of phase 2 (recovery), and the status is read again once the lease is held. Its "Phase order" block has no lease
  step today; the wiring packet (packet 9) adds one between phase 1 and phase 2.
- ADR-006 (crash recovery): its Alternative B (no leases for sessions) rested on single-process runs; this ADR
  enforces that assumption with a lease instead of assuming it. Detection and the `session.interrupted` marker are
  unchanged, and recovery now has a precondition: ownership.
- ADR-009 (delegation): D8's "assumes no other process runs the session" limit is lifted. `delegate:<run id>` owners
  stay; `ClearLeaseOwnedBy` stays for the children of a recovered parent. Known limit L1 (Consequences), not fixed by
  packet 12: it deletes a child's `delegate:` row without judging it, so it can fence a live resumed child. D11 ("No
  run-level lease: each child is an ordinary session with the existing per-session protection") stays true: the lease is
  per session, and this ADR makes that protection real across processes.
- ADR-007 (transport adapters): `serve` is an `internal/server` adapter; the refusal reaches it through the shared
  error-code table (D11), and no lock code lives in the adapter.
- ADR-010 (compaction): D9 (`session compact` takes the exec lease) is the rule here, with `compact:<uuid>`; the
  conservative refuse-on-any-row patch of #458 is relaxed to the judgement table.
- ADR-003 (schema validation): `schemas/cli.json` carries no error-code set, so `session_busy` does not change it; the
  code table is `internal/app/errors.go` with its two mappings (D11).
- Pi's documentation: `sessions.md` and `session-format.md` state no locking rule.
