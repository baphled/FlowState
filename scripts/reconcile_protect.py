"""The effective protected set: a built-in minimum plus the configured additions; no configuration can drop it.

`effective_globs(configured)` returns every entry of `MINIMUM`, compiled into this module so neither `reconcile.json`
nor a candidate can remove it, followed by the configured additions without duplicates. A configuration that is not
a list, lacks a minimum entry, holds a pattern outside the path grammar or yields an empty set raises `ReconcileError`;
there is no fallback. `protects(globs, path, work)` says whether one grammatical literal path falls under any of the
globs, case-folded through `reconcile_glob.match_ci` so a case-insensitive checkout cannot expose `Rules/x` where
`rules/**` is protected; it spends the caller's `Work` meter and raises `WorkExceeded` instead of running long.
"""
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_glob import match_ci
from scripts.reconcile_paths import is_pattern

MINIMUM = (
    ".github/**", ".githooks/**", "rules/**", "Makefile", "go.mod", ".env.ci", "reconcile.json",
    "scripts/base_checkers.py", "scripts/reconcile_*.py", "scripts/admission_*.py", "scripts/review_*.py",
    "scripts/status_issuer.py", "scripts/requirements-admission.txt", "scripts/tests/test_reconcile_*.py",
    "scripts/tests/test_admission_*.py", "scripts/tests/test_base_checkers.py",
)


def effective_globs(configured):
    """Return the minimum and then the configured additions, without duplicates; refuse anything else."""
    if not isinstance(configured, (list, tuple)):
        raise ReconcileError("protected_globs must be a list of patterns")
    dropped = [entry for entry in MINIMUM if entry not in configured]
    malformed = [entry for entry in configured if not is_pattern(entry)]
    if dropped:
        raise ReconcileError("protected_globs drops a built-in minimum entry: " + ", ".join(dropped))
    if malformed:
        raise ReconcileError("protected_globs holds a pattern outside the path grammar: " + repr(malformed[0]))
    effective = tuple(dict.fromkeys(MINIMUM + tuple(configured)))
    if not effective:
        raise ReconcileError("the effective protected set is empty: every transition is refused")
    return effective


def protects(globs, path, work=None):
    """True iff the literal `path` falls under any of the `globs`; ungrammatical input is never protected."""
    return any(match_ci(glob, path, work) for glob in globs)
