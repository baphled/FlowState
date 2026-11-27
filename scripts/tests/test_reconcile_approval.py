import copy
import hashlib
import json
import socket
import unittest
from unittest import mock
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat
from scripts.reconcile_approval import verify_approval
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_disposition import TOPICS
from scripts.review_signature import canonical_payload

PLAN = "dropbox:/FlowState/Knowledge/Plans/PLAN-SDLC-001-r11-continuous-reconciliation.md"
IDS = ("PLAN-SDLC-001", "PLAN-SDLC-001/APPROVAL1")
AUTHOR, REVIEWER, STRANGER = (
    "01a217ce-07d3-738b-9863-28a360afef28", "01a21a01-9dc3-7036-8939-65f53321682d",
    "01a21b02-1111-7036-8939-65f53321682d")
PLAN_BYTES = b"# PLAN-SDLC-001\n\nAPPROVAL1 plan text\n"
STATEMENT = "This packet touches nothing in this area, so there is no behaviour or record for the topic to constrain."
PATHS = ["scripts/reconcile_approval.py", "scripts/tests/test_reconcile_approval.py"]
PACKET = {
    "record_type": "flowstate-work-packet-v1", "packet_id": "PLAN-SDLC-001/APPROVAL1", "title": "Approval verification",
    "plan_ref": PLAN, "plan_sha256": hashlib.sha256(PLAN_BYTES).hexdigest(), "author_session": AUTHOR,
    "allowed_paths": PATHS, "protected_paths": PATHS, "deliverables_new": PATHS, "watched": ["Makefile"],
    "dependencies": [],
    "release_disposition": {topic: {"status": "not-applicable", "statement": STATEMENT} for topic in TOPICS},
}
PACKET_BYTES = json.dumps(PACKET).encode()
RECEIPT = {
    "record_type": "flowstate-plan-approval-v1", "packet_id": "PLAN-SDLC-001/APPROVAL1",
    "packet_ref": "dropbox:/FlowState/Metadata/attempts/2026-11-26-sdlc-reconcile-approval/packet.json",
    "packet_sha256": hashlib.sha256(PACKET_BYTES).hexdigest(), "plan_ref": PLAN,
    "plan_sha256": hashlib.sha256(PLAN_BYTES).hexdigest(),
    "reviewer": {"actor": "Principal-Engineer", "session": REVIEWER, "evidence": "persisted session read-back"},
    "author_session": AUTHOR, "protected_paths_approved": PATHS, "verdict": "ACCEPT",
    "findings": [{"id": "F1", "severity": "P3", "status": "open"}], "approved_at": "2026-11-26T10:00:00Z",
    "delivered_commit": "", "attestation": {"algorithm": "Ed25519", "key_id": "00" * 32, "signature": "00" * 128},
}
PRIVATE, OTHER = Ed25519PrivateKey.generate(), Ed25519PrivateKey.generate()


def public(private):
    return private.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)


def signed(record, private=PRIVATE):
    """Sign `record` with `private` over the canonical payload and return the receipt bytes."""
    record = copy.deepcopy(record)
    record["attestation"] = {"algorithm": "Ed25519", "key_id": hashlib.sha256(public(private)).hexdigest(),
                             "signature": private.sign(canonical_payload(record)).hex()}
    return json.dumps(record).encode()


def receipt_with(**changes):
    return signed(dict(RECEIPT, **changes))


def verify(receipt=None, packet=PACKET_BYTES, plan=PLAN_BYTES, key=public(PRIVATE)):
    return verify_approval(signed(RECEIPT) if receipt is None else receipt, packet, plan, key, IDS)


class AcceptedTest(unittest.TestCase):
    def test_valid_signed_receipt_is_accepted_and_returned(self):
        self.assertEqual(verify()["packet_id"], "PLAN-SDLC-001/APPROVAL1")

    def test_protected_paths_compare_as_a_set(self):
        self.assertEqual(verify(receipt_with(protected_paths_approved=PATHS[::-1] + PATHS[:1]))["verdict"], "ACCEPT")

    def test_open_p3_finding_and_resolved_p2_finding_are_accepted(self):
        findings = [{"id": "F1", "severity": "P3", "status": "open"}, {"id": "F2", "severity": "P2", "status": "resolved"}]
        self.assertEqual(verify(receipt_with(findings=findings))["findings"], findings)

    def test_module_opens_no_connection(self):
        with mock.patch.object(socket, "socket", side_effect=AssertionError("connection")):
            self.assertEqual(verify()["verdict"], "ACCEPT")


class RefusedTest(unittest.TestCase):
    def refused(self, pattern, **kw):
        with self.assertRaisesRegex(ReconcileError, pattern):
            verify(**kw)

    def test_one_changed_byte_of_the_packet_is_refused(self):
        self.refused("packet digest", packet=PACKET_BYTES.replace(b"Approval", b"approval"))

    def test_changed_plan_bytes_are_refused(self):
        self.refused("plan digest", plan=PLAN_BYTES + b"x")

    def test_non_bytes_plan_is_refused(self):
        self.refused("plan bytes", plan="text")

    def test_receipt_signed_by_another_key_is_refused(self):
        self.refused("signature", receipt=signed(RECEIPT, OTHER))

    def test_receipt_changed_after_signing_is_refused(self):
        tampered = json.loads(signed(RECEIPT))
        tampered["verdict"] = "ACCEPT "
        self.refused("invalid|signature", receipt=json.dumps(tampered).encode())

    def test_other_record_type_is_refused_even_when_signed(self):
        self.refused("record_type", receipt=receipt_with(record_type="flowstate-activation-v1"))

    def test_verdict_other_than_accept_is_refused(self):
        for verdict in ("REJECT", "ACCEPT-WITH-FINDINGS", "ACCEPTED"):
            with self.subTest(verdict=verdict):
                self.refused("verdict", receipt=receipt_with(verdict=verdict))

    def test_unresolved_p0_to_p2_finding_is_refused_unless_status_is_exactly_resolved(self):
        for severity in ("P0", "P1", "P2"):
            for status in ("open", "wontfix", "resolved-later", "deferred"):
                with self.subTest(severity=severity, status=status):
                    finding = {"id": "F9", "severity": severity, "status": status}
                    self.refused("unresolved", receipt=receipt_with(findings=[finding]))

    def test_reviewer_equal_to_author_is_refused(self):
        reviewer = dict(RECEIPT["reviewer"], session=AUTHOR)
        self.refused("reviewer session", receipt=receipt_with(reviewer=reviewer))

    def test_receipt_author_differing_from_the_packets_is_refused(self):
        self.refused("author session", receipt=receipt_with(author_session=STRANGER))

    def test_unverified_author_session_is_refused_in_receipt_and_packet(self):
        packet = json.dumps(dict(PACKET, author_session="unverified")).encode()
        self.refused("author_session", receipt=receipt_with(author_session="unverified"))
        self.refused("author_session", packet=packet)

    def test_protected_paths_that_differ_from_the_packets_are_refused(self):
        for approved in (PATHS[:1], PATHS + ["Makefile"], []):
            with self.subTest(approved=approved):
                self.refused("protected paths", receipt=receipt_with(protected_paths_approved=approved))

    def test_receipt_for_another_packet_or_plan_is_refused(self):
        self.refused("packet_id", receipt=receipt_with(packet_id="PLAN-SDLC-001"))
        self.refused("plan_ref", receipt=receipt_with(plan_ref=PLAN.replace("r11", "r12")))

    def test_missing_empty_or_malformed_key_refuses_every_receipt(self):
        for key in (None, b"", b"short", public(PRIVATE)[:31], public(PRIVATE) + b"x", public(PRIVATE).hex(), 7):
            with self.subTest(key=key):
                self.refused("public key", key=key)

    def test_malformed_receipt_or_packet_is_refused(self):
        self.refused("approval receipt", receipt=b"{}")
        self.refused("work-packet record", packet=b"{}")
