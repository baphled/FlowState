"""Resolve the live integration target through each configured source; fail closed."""
import os
import re
import subprocess
import tempfile
from dataclasses import dataclass
from datetime import datetime, timezone
from urllib.parse import quote
from urllib.request import Request
from scripts.admission_transport import open_no_redirect, response_json
from scripts.reconcile_config import ReconcileError, valid_branch, valid_remote_url

SHA = re.compile(r"[0-9a-f]{40}")


@dataclass(frozen=True)
class ResolvedTarget:
    repo: str
    ref: str
    sha: str
    observed_at: str
    sources: tuple


class LiveTransport:
    """Default transport: `git ls-remote` and a GET-only GitHub API; the token is never exposed."""
    def ls_remote(self, url, ref):
        """Ask `url` for `ref` from an empty cwd under a git discovery ceiling; every GIT_* variable and the API token are dropped.

        Other variables (HOME, SSH_AUTH_SOCK) stay because ssh needs them for keys and known_hosts.
        """
        if not valid_remote_url(url):
            raise ReconcileError("remote url must be a real URL")
        env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_") and k not in ("GITHUB_TOKEN", "GH_TOKEN")}
        env.update(GIT_CONFIG_GLOBAL="/dev/null", GIT_CONFIG_SYSTEM="/dev/null", GIT_CONFIG_NOSYSTEM="1",
                   GIT_TERMINAL_PROMPT="0", GIT_CEILING_DIRECTORIES=tempfile.gettempdir())
        with tempfile.TemporaryDirectory() as empty:
            done = subprocess.run(["git", "ls-remote", "--", url, ref], capture_output=True, text=True,
                                  timeout=30, check=True, env=env, cwd=empty)
        rows = [line.split("\t") for line in done.stdout.splitlines()]
        if len(rows) != 1 or len(rows[0]) != 2 or rows[0][1] != ref:
            raise ReconcileError("ls-remote did not return exactly the requested ref")
        return rows[0][0]

    def api(self, repo, branch):
        """Return (repository full_name, branch commit sha)."""
        token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
        if not token:
            raise ReconcileError("missing GitHub token")
        if not valid_branch(branch):
            raise ReconcileError("unsafe branch name")
        base = "https://api.github.com/repos/" + repo
        identity = self._get(base, token)["full_name"]
        return identity, self._get(base + "/branches/" + quote(branch, safe=""), token)["commit"]["sha"]

    def _get(self, url, token):
        request = Request(url, method="GET", headers={
            "Authorization": "Bearer " + token, "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28"})
        with open_no_redirect(request) as response:
            if response.status != 200:
                raise ReconcileError("unexpected GitHub response status")
            return response_json(response)


def _one_source(config, source, ref, branch, transport):
    if source == "ls-remote":
        return transport.ls_remote(config.remote_url, ref)
    identity, sha = transport.api(config.repo, branch)
    if identity != config.repo:
        raise ReconcileError("repository identity mismatch")
    return sha


def resolve(config, context, transport=None, branch=None):
    """Resolve once through every source mandated for `context`; all must succeed and agree."""
    try:
        sources = config.resolve_modes[context]
        branch = config.integration_branch if branch is None else branch
        if not valid_branch(branch):
            raise ReconcileError("unsafe branch name")
        ref = "refs/heads/" + branch
        transport = transport or LiveTransport()
        shas = {s: _one_source(config, s, ref, branch, transport) for s in sources}
        if any(not isinstance(v, str) or not SHA.fullmatch(v) for v in shas.values()):
            raise ReconcileError("malformed SHA")
        if len(set(shas.values())) != 1:
            raise ReconcileError("sources disagree")
        observed = datetime.now(timezone.utc).isoformat(timespec="seconds")
        return ResolvedTarget(config.repo, ref, next(iter(shas.values())), observed, tuple(sources))
    except ReconcileError:
        raise
    except Exception as error:
        raise ReconcileError("target resolution failed: " + type(error).__name__) from None


def resolve_twice(config, context, transport=None, branch=None):
    """Resolve two independent times; a SHA that moved between them raises."""
    first = resolve(config, context, transport, branch)
    second = resolve(config, context, transport, branch)
    if first.sha != second.sha:
        raise ReconcileError("target moved between resolutions")
    return second
