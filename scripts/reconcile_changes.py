"""Changed paths, target tree and one commit's facts from local git or an injected GitHub reader; fail closed.

Both sources expose the same four calls, each taking full 40-character commit SHAs:
`changed_paths(base, head)` (a list of `Change`), `tree_entries(commit)` (every path mapped to its
mode, directories included, so absence is proved by a complete listing), `commit_message(commit)`
(without trailing newlines) and `is_ancestor(commit, tip)`. Any failure raises ReconcileError and the
caller must record indeterminate; no source returns a partial answer.
"""
import os
import subprocess
from dataclasses import dataclass
from functools import wraps
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_target import SHA

MAX_PATHS = 200
ZERO_MODE = "000000"
SPECIAL_MODES = {"120000", "160000"}
RAW_STATUS = {"A": "A", "D": "D", "M": "M", "T": "M"}
API_STATUS = {"added": "A", "copied": "A", "modified": "M", "changed": "M", "removed": "D"}
ANCESTRY = {"ahead": True, "identical": True, "behind": False}


@dataclass(frozen=True)
class Change:
    """One changed path: status A, M or D (a rename is D plus A) and its modes after and before."""
    path: str
    status: str
    mode: str
    old_mode: str

    @property
    def special(self):
        """True for a symlink or gitlink on either side; the caller treats these as not allowed."""
        return bool({self.mode, self.old_mode} & SPECIAL_MODES)


def fail_closed(function):
    """Re-raise anything but ReconcileError as ReconcileError naming only the exception type."""
    @wraps(function)
    def wrapper(*args, **kwargs):
        try:
            return function(*args, **kwargs)
        except ReconcileError:
            raise
        except Exception as error:
            raise ReconcileError(f"{function.__name__} failed: {type(error).__name__}") from None
    return wrapper


def _shas(*values):
    """Require full SHAs; a non-string makes `fullmatch` raise, which `fail_closed` also turns into a refusal."""
    if not all(SHA.fullmatch(value) for value in values):
        raise ReconcileError("a commit must be a full 40-character lowercase SHA")


def _limit(count):
    if count > MAX_PATHS:
        raise ReconcileError(f"more than {MAX_PATHS} changed paths")


def _raw_changes(output):
    """Parse `diff --raw -z` output into Change rows."""
    fields = output.split(b"\0")[:-1]
    if len(fields) % 2:
        raise ReconcileError("malformed raw diff")
    changes = []
    for attrs, name in zip(fields[0::2], fields[1::2]):
        old_mode, mode, _, _, code = attrs.decode("ascii").split()
        if code not in RAW_STATUS:
            raise ReconcileError("unsupported raw diff status")
        changes.append(Change(name.decode("utf-8"), RAW_STATUS[code], mode, old_mode.lstrip(":")))
    return changes


class GitSource:
    """Read from the local repository at `repo`; every inherited GIT_* variable is dropped."""
    def __init__(self, repo):
        self.repo = repo

    def _git(self, *args, check=True):
        env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")
               and k not in ("GITHUB_TOKEN", "GH_TOKEN")}
        # Pin the config the same way reconcile_target.ls_remote does: a user's global
        # diff.relative=true would silently empty changed_paths from a subdirectory repo.
        env.update(GIT_CONFIG_GLOBAL="/dev/null", GIT_CONFIG_SYSTEM="/dev/null",
                   GIT_CONFIG_NOSYSTEM="1", GIT_TERMINAL_PROMPT="0")
        return subprocess.run(["git", "-C", self.repo, *args], capture_output=True, timeout=60,
                              check=check, env=env)

    @fail_closed
    def changed_paths(self, base, head):
        """Changed paths from the merge base of `base` and `head` to `head`, the list GitHub's compare
        returns for `ahead` or `identical` ranges; renames are off, submodule changes are shown, no merge
        base raises, and any other range (`behind` or `diverged`) raises so both sources agree (GitHub
        reports those statuses and the API source refuses them)."""
        _shas(base, head)
        if not self.is_ancestor(base, head):
            raise ReconcileError("range is not ahead or identical: base is not an ancestor of head")
        changes = _raw_changes(self._git("diff", "--raw", "-z", "--no-abbrev", "--no-renames",
                                         "--ignore-submodules=none", base + "..." + head, "--").stdout)
        _limit(len(changes))
        return changes

    @fail_closed
    def tree_entries(self, commit):
        """Every path in the commit's tree, directories included, mapped to its mode."""
        _shas(commit)
        out = self._git("ls-tree", "-r", "-t", "-z", "--full-tree", commit).stdout
        entries = {}
        for row in filter(None, out.split(b"\0")):
            meta, name = row.split(b"\t", 1)
            entries[name.decode("utf-8")] = meta.split()[0].decode("ascii")
        return entries

    @fail_closed
    def commit_message(self, commit):
        """The full message of one commit; a non-commit object raises."""
        _shas(commit)
        out = self._git("show", "-s", "--no-show-signature", "--format=%B", commit + "^{commit}").stdout
        return out.decode("utf-8").rstrip("\n")

    @fail_closed
    def is_ancestor(self, commit, tip):
        """True when `commit` is reachable from `tip`; only exit codes 0 and 1 are answers."""
        _shas(commit, tip)
        code = self._git("merge-base", "--is-ancestor", commit, tip, check=False).returncode
        if code not in (0, 1):
            raise ReconcileError("merge-base could not answer")
        return code == 0


def _api_pairs(files):
    """Flatten compare rows into (path, status); a rename is a delete of the old path plus an add."""
    pairs = []
    for item in files:
        status, name = item.get("status"), item.get("filename")
        if status == "renamed":
            pairs += [(item.get("previous_filename"), "D"), (name, "A")]
        elif status in API_STATUS:
            pairs.append((name, API_STATUS[status]))
        else:
            raise ReconcileError("unsupported compare file status")
    if not all(isinstance(name, str) and name for name, _ in pairs):
        raise ReconcileError("compare row without a path")
    return pairs


def _tree_rows(rows):
    entries = {}
    for row in rows:
        name, mode = row.get("path"), row.get("mode")
        if (type(name), type(mode)) != (str, str) or name in entries:
            raise ReconcileError("invalid tree row")
        entries[name] = mode
    return entries


class ApiSource:
    """Read through `get(path)`: a GET-only reader of the repository API that returns decoded JSON and
    raises on any non-200 reply or network error. The module opens no connection of its own."""
    def __init__(self, get):
        self.get = get

    def _object(self, path):
        record = self.get(path)
        if not isinstance(record, dict):
            raise ReconcileError("GitHub reply is not an object")
        return record

    def _commit(self, commit):
        record = self._object("/commits/" + commit)
        if record.get("sha") != commit:
            raise ReconcileError("commit record does not match the request")
        return record["commit"]

    def _compare(self, base, head):
        return self._object(f"/compare/{base}...{head}")

    @fail_closed
    def changed_paths(self, base, head):
        """Paths changed from `base` to `head`; modes come from both trees. `page` paginates commits only,
        so one reply carries the whole file list, which GitHub caps at 300 rows: above MAX_PATHS, so a
        capped list raises instead of passing. A status of ahead with an empty file list is reported as
        no changes; an absent list raises."""
        _shas(base, head)
        record = self._compare(base, head)
        files = record.get("files")
        if record.get("status") not in ("ahead", "identical") or not isinstance(files, list):
            raise ReconcileError("compare is not ahead or identical, or has no file list")
        pairs = _api_pairs(files)
        _limit(len(pairs))
        if not pairs:
            return []
        before, after = self.tree_entries(base), self.tree_entries(head)
        return [Change(name, status, after.get(name, ZERO_MODE), before.get(name, ZERO_MODE))
                for name, status in pairs]

    @fail_closed
    def tree_entries(self, commit):
        """The commit's complete recursive tree listing; a truncated or mismatched listing raises."""
        _shas(commit)
        tree = self._commit(commit)["tree"]["sha"]
        _shas(tree)
        listing = self._object(f"/git/trees/{tree}?recursive=1")
        if listing.get("sha") != tree or listing.get("truncated") is not False \
                or not isinstance(listing.get("tree"), list):
            raise ReconcileError("tree listing is truncated or does not match the commit")
        return _tree_rows(listing["tree"])

    @fail_closed
    def commit_message(self, commit):
        """The message from the commit endpoint, without trailing newlines."""
        _shas(commit)
        message = self._commit(commit)["message"]
        if not isinstance(message, str):
            raise ReconcileError("commit message is not text")
        return message.rstrip("\n")

    @fail_closed
    def is_ancestor(self, commit, tip):
        """True for compare status ahead or identical, False for behind; any other status raises."""
        _shas(commit, tip)
        status = self._compare(commit, tip).get("status")
        if status not in ANCESTRY:
            raise ReconcileError("compare status is not ahead, identical or behind")
        return ANCESTRY[status]
