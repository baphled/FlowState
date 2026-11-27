"""Load the work-packet record and the plan-approval receipt from stored bytes; closed keys, named refusals.

Both loaders take the raw stored bytes and `packet_ids`, the identifiers the caller parsed from the plan bytes
(the plan front-matter id and every packet heading as `<plan id>/<heading id>`). They return the decoded record,
or raise `ReconcileError` whose message names the defect. A record is refused when it is over 128 KiB, is not
strict JSON (duplicate keys, non-finite numbers and invalid UTF-8 included), is not an object, holds a control
character in any key or string, lacks a key, carries an extra key (at any level) or holds a malformed value.

Pattern lists use `reconcile_paths`: `allowed_paths` needs a literal first segment, `watched` may begin with a
wildcard, and the literal lists hold literal paths. Lists hold at most 32 entries. `protected_paths` and
`protected_paths_approved` are only shape-checked here; later packets compare them as sets. The attestation
envelope is checked for shape only: no signature is verified and no digest is recomputed here.
"""
import re
from datetime import datetime
from scripts.admission_metadata import DIGEST, REFERENCE, AdmissionError
from scripts.admission_transport import strict_json
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_disposition import validate_disposition
from scripts.reconcile_paths import is_allowed_pattern, is_literal_path, is_pattern

MAX_BYTES = 128 * 1024
MAX_ENTRIES = 32
PACKET_TYPE, APPROVAL_TYPE = "flowstate-work-packet-v1", "flowstate-plan-approval-v1"
CONTROL = re.compile(r"[\x00-\x1f\x7f]")
SESSION = re.compile(r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}")
PLAN_REF = re.compile(r"dropbox:/FlowState/Knowledge/Plans/PLAN-[A-Z][A-Z0-9]*-[0-9]{3}-r[0-9]+-[a-z0-9]+(?:-[a-z0-9]+)*\.md")
# The heading part is the plan validator's packet grammar (validate_plan.py PACKET), so a dependency can
# name any packet an accepted plan can hold, such as R1a or W4b.
DEPENDENCY_ID = re.compile(r"PLAN-[A-Z][A-Z0-9]*-[0-9]{3}/[A-Z][A-Z0-9]*[0-9]+[a-z]?(?:\.[0-9]+)?")
TITLE = re.compile(r"\S.{0,199}")
LABEL = re.compile(r"[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}")
SEVERITY = re.compile(r"P[0-9]")
STATUS = re.compile(r"[a-z][a-z-]{0,31}")
VERDICT = re.compile(r"[A-Z][A-Z-]{0,31}")
MOMENT = re.compile(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:Z|\+00:00)")
COMMIT = re.compile(r"[0-9a-f]{40}|")


def _text(pattern):
    return lambda value: isinstance(value, str) and pattern.fullmatch(value) is not None


def _items(check, limit=MAX_ENTRIES):
    return lambda value: isinstance(value, list) and len(value) <= limit and all(map(check, value))


def _object(spec):
    return lambda value: isinstance(value, dict) and set(value) == set(spec) \
        and all(check(value[key]) for key, check in spec.items())


def _moment(value):
    try:
        return _text(MOMENT)(value) and datetime.fromisoformat(value.replace("Z", "+00:00")) is not None
    except ValueError:
        return False


def _literal(value):
    return is_literal_path(value)


def _member(packet_ids):
    allowed = frozenset(packet_ids)
    return lambda value: isinstance(value, str) and value in allowed


def _exactly(constant):
    return lambda value: value == constant


_HEX64, _REF, _SESSION = _text(DIGEST), _text(REFERENCE), _text(SESSION)
_DEPENDENCY = _object({"packet_id": _text(DEPENDENCY_ID), "packet_ref": _REF, "delivery_ref": _REF})
_REVIEWER = _object({"actor": _text(LABEL), "session": _SESSION, "evidence": _text(TITLE)})
_FINDING = _object({"id": _text(LABEL), "severity": _text(SEVERITY), "status": _text(STATUS)})
_ATTESTATION = _object({"algorithm": _exactly("Ed25519"), "key_id": _HEX64,
                        "signature": _text(re.compile(r"[0-9a-f]{128}"))})


def _packet_spec(packet_ids):
    return {
        "record_type": _exactly(PACKET_TYPE), "packet_id": _member(packet_ids), "title": _text(TITLE),
        "plan_ref": _text(PLAN_REF), "plan_sha256": _HEX64, "author_session": _SESSION,
        "allowed_paths": _items(is_allowed_pattern), "protected_paths": _items(_literal),
        "deliverables_new": _items(_literal), "watched": _items(is_pattern), "dependencies": _items(_DEPENDENCY),
        "release_disposition": lambda value: isinstance(value, dict),
    }


def _approval_spec(packet_ids):
    return {
        "record_type": _exactly(APPROVAL_TYPE), "packet_id": _member(packet_ids), "packet_ref": _REF,
        "packet_sha256": _HEX64, "plan_ref": _text(PLAN_REF), "plan_sha256": _HEX64, "reviewer": _REVIEWER,
        "author_session": _SESSION, "protected_paths_approved": _items(_literal), "verdict": _text(VERDICT),
        "findings": _items(_FINDING, 64), "approved_at": _moment, "delivered_commit": _text(COMMIT),
        "attestation": _ATTESTATION,
    }


def _has_control(value):
    """True iff any key or string anywhere in the decoded JSON holds a control character."""
    pending = [value]
    while pending:
        item = pending.pop()
        if isinstance(item, dict):
            pending.extend(item)
            pending.extend(item.values())
        elif isinstance(item, list):
            pending.extend(item)
        elif isinstance(item, str) and CONTROL.search(item):
            return True
    return False


def _decode(raw, label):
    if not isinstance(raw, bytes) or len(raw) > MAX_BYTES:
        raise ReconcileError("%s must be at most 128 KiB of bytes" % label)
    try:
        record = strict_json(raw)
    except AdmissionError as error:
        raise ReconcileError("%s is not strict JSON: %s" % (label, error)) from None
    if not isinstance(record, dict):
        raise ReconcileError("%s root must be a JSON object" % label)
    if _has_control(record):
        raise ReconcileError("%s holds a control character" % label)
    return record


def _load(raw, label, spec):
    record = _decode(raw, label)
    missing, extra = sorted(set(spec) - set(record)), sorted(set(record) - set(spec))
    if missing or extra:
        raise ReconcileError("%s keys must be exactly %s: missing %s, unexpected %s"
                             % (label, ", ".join(spec), missing, extra))
    for key, check in spec.items():
        if not check(record[key]):
            raise ReconcileError("%s %s is invalid" % (label, key))
    return record


def load_packet(raw, packet_ids):
    """Decode a work-packet record (`packet.json`); `packet_ids` are the valid ids from the plan bytes."""
    record = _load(raw, "work-packet record", _packet_spec(packet_ids))
    validate_disposition(record["release_disposition"], record["allowed_paths"])
    return record


def load_approval(raw, packet_ids):
    """Decode a plan-approval receipt (`plan-approval.json`); the signature is not verified here."""
    return _load(raw, "approval receipt", _approval_spec(packet_ids))
