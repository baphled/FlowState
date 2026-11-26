"""Load and validate reconcile.json; every value comes from configuration, none from code."""
import json
import re
from dataclasses import dataclass

CONTEXTS = ("hook", "ci", "runner", "merge_script")
SOURCES = ("ls-remote", "api")
REQUIRED = ("repo", "remote", "integration_branch", "release_branch", "resolve_modes")
OPTIONAL = {"ttl_seconds": 900, "retry": 1, "protected_globs": ()}
REPO = re.compile(r"(?!\.{1,2}/)[A-Za-z0-9_.-]+/(?!\.{1,2}$)[A-Za-z0-9_.-]+")
BRANCH = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_./-]*")
URL = re.compile(r"(?:[A-Za-z][A-Za-z0-9+.-]*://\S+|[A-Za-z0-9_.-]+@[A-Za-z0-9_.-]+:\S+|/\S+)")


class ReconcileError(Exception):
    """Any configuration or resolution failure; callers must block on it."""


@dataclass(frozen=True)
class ReconcileConfig:
    repo: str
    remote_name: str
    remote_url: str
    integration_branch: str
    release_branch: str
    resolve_modes: dict
    ttl_seconds: int = 900
    retry: int = 1
    protected_globs: tuple = ()


def _keys(name, value, required, allowed):
    if not isinstance(value, dict):
        raise ReconcileError(f"{name} must be an object")
    missing = set(required) - set(value)
    unknown = set(value) - set(allowed)
    if missing or unknown:
        raise ReconcileError(f"{name}: missing {sorted(missing)} unknown {sorted(unknown)}")


def _text(name, value):
    if not isinstance(value, str) or not value.strip():
        raise ReconcileError(f"{name} must be a non-empty string")
    return value


def valid_branch(name):
    """True for a conservative ref-name: no '..', '//', leading '-', trailing '/' '.' or '.lock'."""
    return (isinstance(name, str) and bool(BRANCH.fullmatch(name)) and ".." not in name
            and "//" not in name and not name.endswith(("/", ".", ".lock")))


def valid_remote_url(url):
    """True only for a real URL (scheme://, user@host:path or absolute path); never a remote name."""
    return isinstance(url, str) and bool(URL.fullmatch(url)) and not url.startswith(("-", "ext::"))


def _checked(name, value, valid):
    if not valid(value):
        raise ReconcileError(f"{name} is not valid (branch names are conservative; urls are never remote names)")
    return value


def _natural(name, value, minimum):
    if isinstance(value, bool) or not isinstance(value, int) or value < minimum:
        raise ReconcileError(f"{name} must be an integer >= {minimum}")
    return value


def _modes(value):
    _keys("resolve_modes", value, CONTEXTS, CONTEXTS)
    result = {}
    for context in CONTEXTS:
        sources = value[context]
        valid = isinstance(sources, list) and sources and all(s in SOURCES for s in sources)
        if not valid or len(set(sources)) != len(sources):
            raise ReconcileError(f"resolve_modes.{context} must be a unique non-empty subset of {list(SOURCES)}")
        result[context] = tuple(sources)
    return result


def _globs(value):
    if not isinstance(value, list):
        raise ReconcileError("protected_globs must be a list")
    return tuple(_text("protected_globs entry", item) for item in value)


def parse_config(data):
    """Validate a decoded document; unknown or missing keys are rejected."""
    _keys("config", data, REQUIRED, set(REQUIRED) | set(OPTIONAL))
    _keys("remote", data["remote"], ("name", "url"), ("name", "url"))
    if not isinstance(data["repo"], str) or not REPO.fullmatch(data["repo"]):
        raise ReconcileError("repo must be owner/name")
    return ReconcileConfig(
        repo=data["repo"],
        remote_name=_text("remote.name", data["remote"]["name"]),
        remote_url=_checked("remote.url", data["remote"]["url"], valid_remote_url),
        integration_branch=_checked("integration_branch", data["integration_branch"], valid_branch),
        release_branch=_checked("release_branch", data["release_branch"], valid_branch),
        resolve_modes=_modes(data["resolve_modes"]),
        ttl_seconds=_natural("ttl_seconds", data.get("ttl_seconds", OPTIONAL["ttl_seconds"]), 1),
        retry=_natural("retry", data.get("retry", OPTIONAL["retry"]), 0),
        protected_globs=_globs(data.get("protected_globs", [])),
    )


def load_config(path):
    """Read and validate the JSON file at `path`; any failure raises ReconcileError."""
    try:
        with open(path, encoding="utf-8") as handle:
            data = json.load(handle)
    except (OSError, ValueError) as error:
        raise ReconcileError("cannot read reconcile config") from error
    return parse_config(data)
