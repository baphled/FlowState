"""Scope, dependency, duplicate-work and disposition checks for one admitted packet; pure and fail closed.

Each check returns None when the candidate is inside the approved packet and otherwise raises `ReconcileError`
naming the first defect. The caller supplies every input: `packet` and `receipt` are the decoded work-packet record
and the receipt `reconcile_approval.verify_approval` returned, and `Live` holds the live facts. The module opens no
connection, reads no Dropbox path and runs no git command of its own: repository facts come through the injected
`reconcile_changes` source and stored bytes through the injected reader. Any exception but `ReconcileError`, an
exhausted matcher budget included, is re-raised as `ReconcileError` naming only its type. Each function states its
rule; `check_packet` runs them in the plan's fixed order.
"""
import hashlib
from dataclasses import dataclass
from scripts.admission_metadata import DIGEST, SHA, AdmissionError, metadata_reference
from scripts.admission_transport import strict_json
from scripts.reconcile_approval import verify_approval
from scripts.reconcile_changes import fail_closed
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_disposition import validate_disposition
from scripts.reconcile_glob import match
from scripts.reconcile_paths import Work
from scripts.reconcile_protect import effective_globs, protects
from scripts.reconcile_records import DEPENDENCY_ID

MAX_BYTES = 128 * 1024
APPROVAL = "plan-approval.json"
DELIVERY_TYPE = "flowstate-packet-delivery-v1"
LISTED = ("the packet's protected_paths", "the approval receipt")


@dataclass(frozen=True)
class Live:
    """The caller's live facts: the tip SHA, a `reconcile_changes` source, a Dropbox byte reader, the plan bytes read
    live, the pinned plan-approval public key and the packet ids the plan bytes hold."""
    tip: str
    source: object
    read: object
    plan_raw: bytes
    public_key: bytes
    packet_ids: tuple


def _text(pattern):
    """A predicate for one exact-format string field of a stored record."""
    return lambda value: isinstance(value, str) and pattern.fullmatch(value) is not None


DELIVERY = {"record_type": lambda value: value == DELIVERY_TYPE, "packet_id": _text(DEPENDENCY_ID),
            "packet_sha256": _text(DIGEST), "commit": _text(SHA)}


def _decode(raw, label):
    """Decode at most 128 KiB of stored bytes as a strict JSON object, or refuse naming `label`."""
    if not isinstance(raw, bytes) or len(raw) > MAX_BYTES:
        raise ReconcileError("%s must be at most 128 KiB of bytes" % label)
    try:
        record = strict_json(raw)
    except AdmissionError as error:
        raise ReconcileError("%s is not strict JSON: %s" % (label, error)) from None
    if not isinstance(record, dict):
        raise ReconcileError("%s root must be a JSON object" % label)
    return record


def _load(raw, label, spec):
    """Decode a closed-key record: exactly the keys of `spec`, each value passing its predicate."""
    record = _decode(raw, label)
    if set(record) != set(spec):
        raise ReconcileError("%s keys must be exactly %s" % (label, ", ".join(spec)))
    for key, valid in spec.items():
        if not valid(record[key]):
            raise ReconcileError("%s %s is invalid" % (label, key))
    return record


def _change_defect(change, packet, receipt, globs, work):
    """Why one changed path is outside the approved packet, or None."""
    path = change.path
    if change.special:
        return "a symlink or gitlink change is not allowed: " + path
    if not any(match(pattern, path, work) for pattern in packet["allowed_paths"]):
        return "changed path outside allowed_paths: " + path
    if not protects(globs, path, work):
        return None
    listed = (path in packet["protected_paths"], path in receipt["protected_paths_approved"])
    if all(listed):
        return None
    missing = " and ".join(where for ok, where in zip(listed, LISTED) if not ok)
    return "changed protected path absent from " + missing + ": " + path


@fail_closed
def check_changes(packet, receipt, changes, protected_globs):
    """Refuse the first changed path outside the approved packet; return None when every one is inside.

    Every path must match an `allowed_paths` pattern and a symlink or gitlink change is never allowed. The protected
    set is built from the configured `protected_globs` through `reconcile_protect`, so a deficient configuration
    refuses the check; a path under it must be listed in the packet's and in the receipt's protected paths.
    """
    globs, work = effective_globs(protected_globs), Work()
    for change in changes:
        defect = _change_defect(change, packet, receipt, globs, work)
        if defect:
            raise ReconcileError("scope refused: " + defect)


def _refuse(failed, reason):
    """Raise when `failed`; one call per rule keeps each dependency check a readable line."""
    if failed:
        raise ReconcileError("dependency refused: " + reason)


def _check_dependency(entry, live):
    """Apply the section 6 dependency rule to one dependency. Its signed approval, stored as `plan-approval.json`
    beside its `packet_ref`, verifies; `delivery_ref` names a delivery note in that directory with the dependency's
    packet id and the approved packet digest; the named commit is reachable from the tip; that commit's single
    `Project-Metadata` trailer resolves into that directory; and the admission record the trailer names binds the
    same packet digest or the signed approval names the commit as `delivered_commit`."""
    directory = entry["packet_ref"].rpartition("/")[0]
    _refuse(entry["delivery_ref"].rpartition("/")[0] != directory, "delivery_ref is outside the directory of packet_ref")
    packet_raw = live.read(entry["packet_ref"])
    approval = verify_approval(live.read(directory + "/" + APPROVAL), packet_raw, live.plan_raw, live.public_key,
                               live.packet_ids)
    delivery = _load(live.read(entry["delivery_ref"]), "delivery note", DELIVERY)
    digest = hashlib.sha256(packet_raw).hexdigest()
    identified = (approval["packet_id"], delivery["packet_id"], delivery["packet_sha256"])
    _refuse(identified != (entry["packet_id"], entry["packet_id"], digest),
             "the approval or the delivery note does not name the dependency's packet and packet digest")
    commit = delivery["commit"]
    _refuse(not live.source.is_ancestor(commit, live.tip), "the delivery commit is not reachable from the live tip")
    try:
        trailer = metadata_reference(live.source.commit_message(commit))
    except AdmissionError as error:
        raise ReconcileError("dependency refused: the delivery commit has no single valid Project-Metadata trailer: %s"
                             % error) from None
    _refuse(trailer.rpartition("/")[0] != directory, "the delivery commit's Project-Metadata trailer resolves elsewhere")
    named = approval["delivered_commit"] == commit  # signed evidence on its own: then no admission record is read
    _refuse(not named and _decode(live.read(trailer), "admission record").get("packet_sha256") != digest,
            "neither the admission record binds the packet digest nor the approval names the commit")


@fail_closed
def check_dependencies(packet, live):
    """Refuse the first dependency that does not satisfy the section 6 dependency rule; None when all are delivered."""
    for entry in packet["dependencies"]:
        _check_dependency(entry, live)


@fail_closed
def check_duplicates(deliverables_new, live):
    """Refuse the first new deliverable already in the tree of the live tip; a lookup error raises. Before merge only."""
    entries = live.source.tree_entries(live.tip)
    present = [path for path in deliverables_new if path in entries]
    if present:
        raise ReconcileError("duplicate work refused: deliverable already on the live target: " + ", ".join(present))


@fail_closed
def check_packet(packet, receipt, changes, protected_globs, live, release_sensitive_paths=(), before_merge=False):
    """Run every packet check in the fixed order; `before_merge` says whether duplicate work is checked."""
    check_changes(packet, receipt, changes, protected_globs)
    check_dependencies(packet, live)
    if before_merge:
        check_duplicates(packet["deliverables_new"], live)
    validate_disposition(packet["release_disposition"], packet["allowed_paths"], release_sensitive_paths)
