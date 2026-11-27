"""Classify upstream drift of the live target against a packet's watched patterns; a pure function with no I/O.

`classify(previous, fresh, paths, watched)` returns `(outcome, reasons)`. `outcome` is one of `current`, `non_material`,
`material` or `indeterminate`; `reasons` is a sorted list of upper-case codes naming why. Rules, in this order:

1. a SHA that is not 40 lowercase hex characters: indeterminate (fail closed before any comparison);
2. equal SHAs: current;
3. `watched` not a list of at most 32 grammatical patterns, `paths` absent or not a list of strings, more than 200
   paths, or an exhausted matcher work budget: indeterminate;
4. a path that fails the `reconcile_paths` literal grammar, or matches a watched pattern: material;
5. otherwise: non_material.

An empty `paths` list with different SHAs, or an empty `watched` list, is non_material. Matching is case-sensitive
through `reconcile_glob.match`; the classifier never reads the repository, the network or the clock.
"""
import re
from scripts.reconcile_glob import match
from scripts.reconcile_paths import Work, WorkExceeded, is_literal_path, is_pattern

MAX_PATHS, MAX_WATCHED = 200, 32
_SHA = re.compile(r"[0-9a-f]{40}")


def _valid_sha(value):
    return isinstance(value, str) and _SHA.fullmatch(value) is not None


def _valid_list(value, limit):
    return isinstance(value, list) and len(value) <= limit


def _precheck(previous, fresh, paths, watched):
    """The first refusing `(outcome, reasons)` for malformed input, or None when the rules may proceed."""
    if not (_valid_sha(previous) and _valid_sha(fresh)):
        return "indeterminate", ["SHA_MALFORMED"]
    if previous == fresh:
        return "current", ["SHA_EQUAL"]
    if not (_valid_list(watched, MAX_WATCHED) and all(is_pattern(item) for item in watched)):
        return "indeterminate", ["WATCHED_MALFORMED"]
    if not (isinstance(paths, list) and all(isinstance(item, str) for item in paths)):
        return "indeterminate", ["UPSTREAM_PATHS_UNKNOWN"]
    if len(paths) > MAX_PATHS:
        return "indeterminate", ["TOO_MANY_PATHS"]
    return None


def _reason(path, watched, work):
    """The material reason for one changed path, or None when it is outside every watched pattern."""
    if not is_literal_path(path):
        return "UNGRAMMATICAL_PATH"
    if any(match(pattern, path, work) for pattern in watched):
        return "WATCHED_PATH_CHANGED"
    return None


def classify(previous, fresh, paths, watched):
    """Classify the move from `previous` to `fresh`; see the module documentation for the rules and reason codes."""
    refused = _precheck(previous, fresh, paths, watched)
    if refused:
        return refused
    work = Work()  # one budget for the whole classification, so pathological input fails closed quickly
    try:
        found = {_reason(path, watched, work) for path in paths} - {None}
    except WorkExceeded:
        return "indeterminate", ["MATCH_WORK_EXCEEDED"]
    if found:
        return "material", sorted(found)
    return "non_material", ["NO_WATCHED_CHANGE"]
