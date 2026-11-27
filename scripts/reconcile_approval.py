"""Verify a signed plan-approval receipt against a pinned public key and bind it to the exact inputs.

`verify_approval` takes only values the caller already holds, so it opens no connection and reads no
Dropbox file: the receipt bytes, the stored packet-record bytes, the plan bytes read live, the pinned
Ed25519 public key (32 raw bytes, or None while no key is pinned) and `packet_ids`, the identifiers the
caller parsed from the plan bytes. It returns the decoded receipt when every check passes and raises
`ReconcileError` naming the first defect otherwise.

The key is checked first: a missing, empty or malformed key refuses every receipt. The receipt and the
packet are then decoded by `reconcile_records` and the signature is verified over the canonical payload by
`review_signature`. The bindings follow: both digests equal the sha256 of the supplied bytes; the receipt
names the packet's id and plan; the verdict is `ACCEPT`; no P0 to P2 finding has a status other than
exactly `resolved`; the reviewer session differs from the author session; the author session equals the
packet's; and the approved protected paths equal the packet's as sets. A session that is not a session id
(such as `unverified`) is refused by the record loaders.
"""
import hashlib
from scripts.admission_metadata import AdmissionError
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_records import load_approval, load_packet
from scripts.review_signature import verify_signature

KEY_BYTES = 32
GATED_SEVERITIES = frozenset(("P0", "P1", "P2"))
RESOLVED = "resolved"


def _key_hex(public_key):
    """Hex form of the pinned key, which `verify_signature` expects; refuse a missing or malformed key."""
    if not isinstance(public_key, bytes) or len(public_key) != KEY_BYTES:
        raise ReconcileError("plan-approval public key is missing or malformed: every receipt is refused")
    return public_key.hex()


def _digest(raw):
    return hashlib.sha256(raw).hexdigest()


def _unresolved(receipt):
    return sorted(item["id"] for item in receipt["findings"]
                  if item["severity"] in GATED_SEVERITIES and item["status"] != RESOLVED)


def _defects(receipt, packet, packet_raw, plan_raw):
    """Pairs of (the check failed, the refusal), in the order they are reported."""
    return (
        (receipt["packet_sha256"] != _digest(packet_raw), "packet digest differs from the stored packet bytes"),
        (receipt["plan_sha256"] != _digest(plan_raw), "plan digest differs from the plan bytes read live"),
        (receipt["packet_id"] != packet["packet_id"], "packet_id differs from the packet record"),
        (receipt["plan_ref"] != packet["plan_ref"], "plan_ref differs from the packet record"),
        (receipt["verdict"] != "ACCEPT", "verdict is not ACCEPT"),
        (bool(_unresolved(receipt)), "unresolved P0 to P2 findings: %s" % ", ".join(_unresolved(receipt))),
        (receipt["reviewer"]["session"] == receipt["author_session"], "reviewer session equals the author session"),
        (receipt["author_session"] != packet["author_session"], "author session differs from the packet's"),
        (set(receipt["protected_paths_approved"]) != set(packet["protected_paths"]),
         "approved protected paths differ from the packet's"),
    )


def verify_approval(receipt_raw, packet_raw, plan_raw, public_key, packet_ids):
    """Return the decoded receipt if it is signed under `public_key` and bound to the packet and plan bytes."""
    key = _key_hex(public_key)
    if not isinstance(plan_raw, bytes):
        raise ReconcileError("plan bytes must be bytes")
    receipt, packet = load_approval(receipt_raw, packet_ids), load_packet(packet_raw, packet_ids)
    try:
        verify_signature(receipt, key)
    except AdmissionError as error:
        raise ReconcileError("approval receipt signature refused: %s" % error) from None
    for failed, refusal in _defects(receipt, packet, packet_raw, plan_raw):
        if failed:
            raise ReconcileError("approval receipt refused: " + refusal)
    return receipt
