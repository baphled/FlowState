"""Build, name, digest and verify reconciliation evidence records.

A record is canonical JSON (sorted keys, `,` and `:` separators, ASCII escapes) whose
`digest` is the sha256 of the same bytes without the digest field. Verification refuses
stale, future-dated, wrong-identity, target-advanced, non-canonical and tampered records.
This module only builds and checks bytes; it never reads or writes any storage.
"""
import hashlib
import json
import re
from datetime import datetime, timezone
from scripts.admission_metadata import AdmissionError
from scripts.admission_transport import strict_json
from scripts.reconcile_config import REPO, ReconcileError
from scripts.reconcile_target import SHA

RECORD_TYPE = "flowstate-reconciliation-v1"
FUTURE_SECONDS = 300
MAX_BYTES = 128 * 1024
HEX64 = re.compile(r"[0-9a-f]{64}")
PATTERNS = {
    "record_type": re.compile(re.escape(RECORD_TYPE)),
    "checkpoint": re.compile(r"[a-z][a-z0-9-]{0,31}"),
    "repository": REPO,
    "ref": re.compile(r"refs/heads/[A-Za-z0-9_][A-Za-z0-9_./-]*"),
    "previous_sha": SHA,
    "resolved_sha": SHA,
    "candidate_sha": SHA,
    "packet_id": re.compile(r"[A-Za-z0-9._/-]{1,128}"),
    "packet_sha256": HEX64,
    "plan_sha256": HEX64,
    "outcome": re.compile(r"current|non_material|material|indeterminate"),
    "observed_at": re.compile(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\+00:00|Z)"),
    "digest": HEX64,
}
LISTS = {"upstream_paths": (re.compile(r"[^\x00-\x1f\x7f]{1,4096}"), 200),
         "reasons": (re.compile(r"[A-Z][A-Z0-9_]{0,63}"), 32)}
KEYS = frozenset(PATTERNS) | frozenset(LISTS)
IDENTITY = frozenset(("checkpoint", "candidate_sha", "packet_id", "packet_sha256", "plan_sha256"))


def canonical_bytes(record):
    """The one wire form of a record; any other serialisation is refused on load."""
    return json.dumps(record, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("ascii")


def digest_of(record):
    """sha256 over the canonical bytes of the record without its digest field."""
    return hashlib.sha256(canonical_bytes({k: v for k, v in record.items() if k != "digest"})).hexdigest()


def file_name(record):
    """Flat Metadata file name: reconciliation-<checkpoint>-<first 12 digest characters>.json."""
    check_shape(record)
    return "reconciliation-%s-%s.json" % (record["checkpoint"], record["digest"][:12])


def moment(text):
    """Parse a validated observed_at; an impossible calendar value raises."""
    try:
        return datetime.fromisoformat(text)
    except ValueError:
        raise ReconcileError("evidence observed_at is not a real time") from None


def check_shape(record):
    """Exact key set plus a format check on every value; raises ReconcileError."""
    if not isinstance(record, dict) or set(record) != KEYS:
        raise ReconcileError("evidence keys must be exactly " + ", ".join(sorted(KEYS)))
    for key, pattern in PATTERNS.items():
        if not isinstance(record[key], str) or not pattern.fullmatch(record[key]):
            raise ReconcileError("evidence %s is malformed" % key)
    for key, (pattern, limit) in LISTS.items():
        items = record[key]
        if not isinstance(items, list) or len(items) > limit or \
                not all(isinstance(i, str) and pattern.fullmatch(i) for i in items):
            raise ReconcileError("evidence %s is malformed" % key)
    moment(record["observed_at"])


def seal(fields):
    """Validate the fields (all keys except record_type and digest) and add both."""
    record = dict(fields, record_type=RECORD_TYPE, digest="0" * 64)
    check_shape(record)
    record["digest"] = digest_of(record)
    return record


def load(raw):
    """Decode canonical bytes into a self-consistent record, without any live context."""
    if not isinstance(raw, bytes) or len(raw) > MAX_BYTES:
        raise ReconcileError("evidence must be at most 128 KiB of bytes")
    try:
        record = strict_json(raw)
    except AdmissionError:
        raise ReconcileError("evidence is not valid JSON") from None
    check_shape(record)
    if raw != canonical_bytes(record):
        raise ReconcileError("evidence is not canonical")
    if record["digest"] != digest_of(record):
        raise ReconcileError("evidence digest does not match its content")
    return record


def check_fresh(record, ttl, now):
    """Reject a record older than `ttl` seconds or dated beyond the future bound."""
    age = (now - moment(record["observed_at"])).total_seconds()
    if age > ttl:
        raise ReconcileError("evidence is stale")
    if -age > FUTURE_SECONDS:
        raise ReconcileError("evidence is dated in the future")


def verify(raw, expected, target, ttl, now=None):
    """Accept `raw` only for exactly this identity, this live target and this moment.

    `expected` names the five identity keys; repository and ref come from `target`, the
    freshly resolved ResolvedTarget. Order: integrity, identity, target advance, freshness.
    """
    if isinstance(ttl, bool) or not isinstance(ttl, int) or ttl < 1:
        raise ReconcileError("ttl must be a positive integer")
    if not isinstance(expected, dict) or set(expected) != IDENTITY:
        raise ReconcileError("expected identity must name exactly " + ", ".join(sorted(IDENTITY)))
    record = load(raw)
    wanted = dict(expected, repository=target.repo, ref=target.ref)
    for key in sorted(wanted):
        if record[key] != wanted[key]:
            raise ReconcileError("evidence %s does not match" % key)
    if record["resolved_sha"] != target.sha:
        raise ReconcileError("target advanced since evaluation")
    check_fresh(record, ttl, now or datetime.now(timezone.utc))
    return record
