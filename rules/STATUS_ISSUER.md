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

## Verification

From repository root, install `scripts/requirements-admission.txt` in an isolated
environment, then run the synthetic suite without live credentials:

```bash
python3 -B -m unittest scripts.tests.test_app_credentials scripts.tests.test_status_issuer -v
```

See GitHub's [App JWT](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app),
[installation token](https://docs.github.com/en/rest/apps/apps#create-an-installation-access-token-for-an-app)
and [protected status source](https://docs.github.com/en/rest/branches/branch-protection#get-status-checks-protection) contracts.
