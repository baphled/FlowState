"""Verify the exact release candidate, version and artefacts before and after publication.

Every fact comes from the caller or from an injected transport; this module opens no connection
and reads no Dropbox evidence. The transport provides two calls:

* ``api(repo, branch)`` returns ``(repository full name, branch tip sha)``;
* ``release_assets(repo, tag)`` returns the names of the assets carried by the published release.

The repository and release branch come from the reconcile configuration. Each check returns a
``Decision``: ``allowed`` or a block that names its reason. Any transport failure or malformed
input blocks; nothing is retried or assumed.
"""
import re
from dataclasses import dataclass
from scripts.reconcile_config import valid_branch

SHA = re.compile(r"[0-9a-f]{40}")
DIGEST = re.compile(r"[0-9a-f]{64}")
VERSION = re.compile(r"(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?")
LISTS = (list, tuple, set, frozenset)


@dataclass(frozen=True)
class Decision:
    """The outcome of one check: ``allowed`` or a block whose ``reason`` names the failure."""
    allowed: bool
    reason: str


OK = Decision(True, "ok")


def _is(pattern, value):
    return isinstance(value, str) and bool(pattern.fullmatch(value))


def _first_block(rules):
    """Return the block for the first rule whose flag is set, otherwise allow."""
    return next((Decision(False, reason) for failed, reason in rules if failed), OK)


def _sequence(checks):
    """Run the checks in order and stop at the first block."""
    for check in checks:
        decision = check()
        if not decision.allowed:
            return decision
    return OK


def check_candidate(config, candidate, transport):
    """Allow only a candidate equal to the API-resolved tip of the configured release branch."""
    early = _first_block(((not valid_branch(config.release_branch), "release-branch-invalid"),
                          (not _is(SHA, candidate), "candidate-malformed")))
    if not early.allowed:
        return early
    try:
        identity, tip = transport.api(config.repo, config.release_branch)
    except Exception:
        return Decision(False, "tip-unresolved")
    return _first_block(((identity != config.repo, "repository-identity-mismatch"),
                         (not _is(SHA, tip), "tip-malformed"), (tip != candidate, "candidate-not-tip")))


def check_version(tag, computed):
    """Allow only a tag (with or without one leading ``v``) equal to the dry-run computed version."""
    bare = tag[1:] if isinstance(tag, str) and tag.startswith("v") else tag
    return _first_block(((not _is(VERSION, computed), "computed-version-malformed"),
                         (not _is(VERSION, bare), "tag-malformed"), (bare != computed, "tag-version-mismatch")))


def _digests(value):
    return isinstance(value, dict) and bool(value) and all(isinstance(k, str) and _is(DIGEST, v) for k, v in value.items())


def check_checksums(expected, observed):
    """Allow only artefacts whose sha256 digests equal the build's, over exactly the same names."""
    early = _first_block(((not _digests(expected), "build-checksums-invalid"),
                          (not _digests(observed), "artefact-checksums-invalid")))
    if not early.allowed:
        return early
    if set(expected) != set(observed):
        return Decision(False, "artefact-set-mismatch")
    bad = sorted(name for name in expected if expected[name] != observed[name])
    return Decision(False, "checksum-mismatch: " + ", ".join(bad)) if bad else OK


def check_published(config, tag, expected_assets, transport):
    """Allow only a published release that carries every expected asset."""
    if not isinstance(expected_assets, LISTS) or not expected_assets or not all(isinstance(n, str) for n in expected_assets):
        return Decision(False, "expected-assets-unconfigured")
    try:
        published = transport.release_assets(config.repo, tag)
    except Exception:
        return Decision(False, "release-unresolved")
    if not isinstance(published, LISTS) or not all(isinstance(name, str) for name in published):
        return Decision(False, "release-assets-malformed")
    missing = sorted(set(expected_assets) - set(published))
    return Decision(False, "asset-missing: " + ", ".join(missing)) if missing else OK


def verify_before(config, candidate, tag, version, expected, observed, transport):
    """Pre-publication gate: candidate is the tip, tag matches the dry run, checksums match the build."""
    return _sequence((lambda: check_candidate(config, candidate, transport), lambda: check_version(tag, version),
                      lambda: check_checksums(expected, observed)))


def verify_after(config, tag, version, expected_assets, transport):
    """Post-publication gate: the published tag matches the computed version and carries every asset."""
    return _sequence((lambda: check_version(tag, version),
                      lambda: check_published(config, tag, expected_assets, transport)))
