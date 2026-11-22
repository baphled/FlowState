# Trusted status issuer

`app_credentials` and `status_issuer` are source components. They do not install
trusted execution, branch protection or an atomic merge authority.

## Credentials

A trusted publisher receives the GitHub App RSA private key, positive App,
installation/repository IDs and exact repository name. Each execution authenticates
the App/installation and requests a repository-scoped `statuses: write` token.
Only optional `metadata: read` is allowed alongside it; expiry cannot exceed one
hour. Invalid minted tokens are revoked. Call `close()` in `finally`; revocation
failure is fatal and retains the token for retry. Never persist installation
tokens or expose issuer credentials to candidate code or dependencies.

The separate read client needs contents, PR and branch-protection
(`administration: read`) access. It has no status-publisher key. Review-attestor
public keys are independent of the App private key. HTTP redirects are rejected.

## Protected source

Every success requires live `next` protection pinning `flowstate/merge-admission`
to the exact App ID, strict required `Build`, admin enforcement and linear history.
Missing/wrong issuer, unavailable protection or publication errors fail closed.
Pending/failure can revoke a prior success even if protection cannot be read.
Returned status, context and bot identity must match the expected publisher.

A trusted merge boundary must revalidate and retain exact evidence and prevent
stale-success reuse; these modules alone cannot enforce that boundary.

## Packaged development runner

From a reviewed source commit, `make package-admission-runner` produces
`build/admission-runner.pyz` containing committed admission modules, their source
SHA and pinned dependency manifest. It contains no credentials or candidate code.
The installed service runs this package, independently of feature worktrees.

Create `~/.config/flowstate/admission.json` and the referenced RSA key with mode
0600, owned by your user. Supply the existing qualified credentials and separate
public authorities; do not create replacement signing identities to pass a check:

```json
{
  "repository": "baphled/FlowState",
  "repository_id": 1191083392,
  "app_id": 5728089,
  "installation_id": 181235940,
  "github_read_token": "existing-read-token-with-protection-read-access",
  "dropbox_read_token": "existing-dropbox-read-token",
  "private_key_file": "/absolute/path/to/existing-app-key.pem",
  "review_public_key": "existing-64-character-lowercase-hex-public-key",
  "execution_public_key": "different-existing-64-character-lowercase-hex-public-key"
}
```

Where no Dropbox API token exists, the runner may instead read the owner-private
local Dropbox sync tree: set `dropbox_local_path` to the directory that contains
`FlowState/` (owner-only mode, e.g. `~/Dropbox`) and omit `dropbox_read_token`;
exactly one of the two must be configured. The local adapter is read-only and
rejects references that escape the tree's `FlowState/` metadata root.

On Linux with a running systemd user manager and Python venv support:

```sh
make install-admission-runner
make session-start
make admission-runner-status
journalctl --user -u flowstate-admission.service
make admission-runner-stop
```

Installation validates config, installs pinned dependencies in a venv and registers
the user unit. Startup authenticates the App/protected source before notifying
systemd readiness; failure/timeout returns nonzero. A crashed or failed runner is
restarted for ever, every 30 seconds, with no start limit, so a persistent fault
shows as `activating (auto-restart)` and never as `failed`: `make admission-runner-status`
prints ActiveState, SubState, NRestarts and Result; also check
`journalctl --user -u flowstate-admission.service`. Installing does not restart a running
publisher; stop and start it to run newly installed code.
Every 15 seconds all ready next PRs are evaluated in full, including PRs without a review.
Unexpected exceptions and SIGTERM attempt exact-head failure publication and token
cleanup. Per-PR errors are logged/retried while other PRs continue; single-shot
`--once`/`--pr` errors and startup failures exit nonzero. Inspect journald.

Publish on change, for heads that are not admitted only. The publisher keeps, for the life
of the process, a record per candidate head of the last (state, description) it published and
when. A `success` is never deduplicated and is always withdrawn first: an evaluation begins
with the in-progress `pending` marker whenever the head has no record or its record is a
`success`, exactly as before, so a stale `success` never stands longer than it did. Only a head
whose record is a non-success verdict (`failure`, or a waiting `pending`) is quiet: the marker
is skipped and an unchanged verdict is not repeated, so a head that is not admitted publishes
once per change.

An admitted head is re-published every cycle as before: `pending` then `success`, two statuses
every 15 seconds plus the evaluation time. The merge-time `pending` flap therefore remains.

GitHub keeps at most 1000 statuses per commit and context, and at the limit it refuses a `pending`
and a `failure` (HTTP 422), so a standing `success` could no longer be withdrawn. A head must never
get near the limit holding a success, so the publisher keeps a status budget per head
(`STATUS_BUDGET`, 800, below `STATUS_LIMIT`, 1000). On first sight of a head, including after a restart,
it reads how many statuses the context already holds on the commit: `GET /commits/<sha>/statuses`,
newest first, paged by 100 and filtered to the context locally because GitHub offers no filter. That is
one request for a head with under 100 statuses in all, about nine at the budget and about eleven at the
limit. Afterwards the publisher counts the publications that returned; the count is kept outside the
record so the ten-minute expiry cannot reset it. A publication that raised is never counted from memory:
the count is dropped and read from GitHub again before the next one, so an outage cannot use up a head.
While the answer is unreadable, malformed or longer than 30 pages, every status for the head becomes
`failure: Admission status count is unavailable; retrying` (published on change, never a `success`), and
the count is read again each cycle (at most once per 10 seconds per head, `READ_INTERVAL`, so a retry loop or
two calls in one cycle cost one read); a call inside that window with no held count is a failure, not a
remembered number; the head is not ended.

The publication that would take a head past the budget is replaced by one final `failure`,
`Admission window for this commit is exhausted; push a new commit to continue`. Once that failure is
published, nothing more is published for that head and never a `success`, whatever the evaluation says,
until the head's SHA changes or it leaves the open candidates. The 200 statuses of reserve cover the
final failure; if it raises it is retried each cycle until it lands, and the head is not ended before it
does. After a restart the same head is exhausted again from the GitHub count: nothing is published when
the newest status of the context is already that failure from the publisher, otherwise one failure is. A
head GitHub already holds at the limit is left alone, because nothing can be published to it. Below the
budget nothing changes. A `--once` run on an exhausted head publishes nothing, so read the pull request
page for the verdict.

The record is invalidated before every publication and written only after it returned. Any
exception, including one raised after GitHub applied the POST and KeyboardInterrupt, leaves no
record, so the next cycle publishes the marker and the verdict unconditionally; a revoking
`failure` is never skipped because of a publication that failed locally. A record older than
ten minutes (`RECORD_TTL`) counts as absent, so the publisher re-asserts its status at least
that often and reconciles with anything another writer posted to the context. Heads that are no
longer the head of an open candidate PR are dropped at each scan (with their counts and clocks), and
a restart starts empty.
Every `success` still passes the live branch-protection check inside the issuer.

The record is keyed by head because GitHub keeps one status per head and context, while
verdicts are per PR. Two open PRs on one head share the record, which always mirrors the newest
status posted, so one PR's `success` never counts as published for the other. After the first
cycle they publish three statuses per cycle (`success`, `pending`, `failure`) and the context
ends each cycle on the verdict of the PR scanned last. The base branch behaves the same way
(four statuses per cycle), so a PR that is not admitted can ride on the other PR's `success`
for part of a cycle exactly as before; the review record binds the PR number, so only one of
them can be admitted.

Waiting is `pending`, a defect is `failure`. `pending: Awaiting an independent exact-head
agent review` means nothing in the PR description resembles a review line. A line that looks like one
(any case, spacing before the colon, `Agent Review:`, `Agent_Review:`, leading blanks, or a look-alike hyphen
such as U+2011 or U+2013) but is not exactly `Agent-Review: <reference>` is a `failure` naming the spelling,
never the waiting `pending`. The look-alike match runs on a normalised copy (NFKC, any dash or minus folded to
`-`, invisible format characters removed); the copy is never used to accept a line.
`pending: Waiting for required check <name>` means a required check is not reported, queued
or in progress on the head. More than one line, a malformed reference, or a required check
that finished without success is a `failure`. A waiting `pending` is a non-success record: the
marker is skipped while it stands and it is published once per change. A pending candidate is
not admitted: `--once`/`--pr` still exit nonzero. They exit 0 only when the evaluated `success` is what the
publisher actually published for the head: an exhausted head (nothing published) or an unknown status count
(`failure: ... count is unavailable`) is a nonzero exit, as is any other substituted failure. The service loop
ignores the result and is unchanged. An interrupt (`KeyboardInterrupt`, `SystemExit`) always propagates out of
the candidate, the scan and the service loop, after a best-effort `failure`; the reason logged for a failed
candidate is one printable line of at most 200 characters. The publisher itself condenses every description to
the text GitHub keeps (one printable line, at most 140 characters) before it compares it with the last
publication, so a finding published by the evaluation and again by the runner reads the same and is posted once,
not once per cycle.

Waiting for a required check has a deadline. The publisher records when it first saw a head waiting for
a given check; after 60 minutes (`CHECK_DEADLINE`) of continuous waiting for the SAME check it publishes
`failure: required check <name> has not reported after 60 minutes` instead of the waiting `pending`, so
a workflow that never triggers is visible. The failure is not sticky: when the check reports, or any
other verdict is reached, the next evaluation publishes whatever is true and the clock is cleared; a
different unfinished check starts its own clock. The clock is kept outside the record (it survives the
ten-minute expiry) but only in the process, so a restart resets it. The review wait (`Awaiting an
independent exact-head agent review`) never times out: a review can legitimately take long and is the
operator's own action. Pending and failure both block the merge; the deadline only tells the truth.

In service mode an `AdmissionError` raised by a scan (for example a transient GitHub
API error while listing pull requests) is logged to stderr as
`scan failed (<n> consecutive): <reason>`; the loop sleeps and tries again and the count
resets after a scan completes. A scan with at least one candidate in which every candidate raised
(credential, GitHub or publication faults, as opposed to a PR that was simply not admitted and whose
verdict was published) is a failed scan and raises, while one healthy candidate keeps it good. After
20 consecutive failed scans (`SCAN_FAILURE_LIMIT`, about five minutes) the service exits non-zero, so
systemd restarts it, startup verification runs again and a persistent failure shows in the restart
counter. Unexpected exceptions, SIGTERM and KeyboardInterrupt stop the service at once, and `--once`,
`--pr`, `--check-config` and
`--watch` are unchanged.

The separate CI `Merge Admission Result` job is a publisher liveness monitor. It runs only for
non-draft pull requests into `next`, the only ones the publisher reports on. After
Build it waits at most 120 seconds for any `flowstate/merge-admission` status
from the App for the exact head. A status in any state (success, pending, failure
or error) passes the job and is written to the step summary: the required status
context, not this job, decides the merge, so the job is expected to be green
before a review is attested. No status from the publisher by the deadline fails the
job with a pointer to `systemctl --user status flowstate-admission.service`, as
does a status from any other creator. It cannot satisfy or replace the
App-pinned required status. It follows Build to avoid an admission/Build cycle.
This polling service does not provide exclusive atomic merge authority. Stop an
older admission daemon before adopting it; do not run competing publishers.

## Verification

From repository root, install `scripts/requirements-admission.txt` in an isolated
environment, then run the synthetic suite without live credentials:

```bash
python3 -B -m unittest scripts.tests.test_app_credentials scripts.tests.test_status_issuer -v
```

See GitHub's [App JWT](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app),
[installation token](https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app)
and [protected status source](https://docs.github.com/en/rest/branches/branch-protection#get-status-checks-protection) contracts.
