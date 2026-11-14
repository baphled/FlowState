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
systemd readiness; failure/timeout returns nonzero. Crashes have bounded restarts.
Every 15 seconds all ready next PRs are evaluated, including missing-review failures.
Unexpected exceptions and SIGTERM attempt exact-head failure publication and token
cleanup. Per-PR errors are logged/retried while other PRs continue; single-shot
`--once`/`--pr` errors and startup/list failures exit nonzero. Inspect journald.

The separate CI `Merge Admission Result` check waits at most 120 seconds after
Build for a terminal App-issued result. Missing, stuck, failed or wrong-publisher
results fail CI visibly. It is diagnostic: it cannot satisfy or replace the
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
