import copy
import json
import unittest
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_disposition import TOPICS
from scripts.reconcile_records import load_approval, load_packet

PLAN = "dropbox:/FlowState/Knowledge/Plans/PLAN-SDLC-001-r11-continuous-reconciliation.md"
IDS = ("PLAN-SDLC-001", "PLAN-SDLC-001/RECORDS1", "PLAN-SDLC-001/MATCHER1")
SESSION, OTHER = "01a217ce-07d3-738b-9863-28a360afef28", "01a21a01-9dc3-7036-8939-65f53321682d"
ATTEMPT = "dropbox:/FlowState/Metadata/attempts/2026-11-26-sdlc-reconcile-records/"
STATEMENT = "This packet touches nothing in this area, so there is no behaviour or record for the topic to constrain."
PACKET = {
    "record_type": "flowstate-work-packet-v1", "packet_id": "PLAN-SDLC-001/RECORDS1", "title": "Work-packet record",
    "plan_ref": PLAN, "plan_sha256": "ab" * 32, "author_session": SESSION,
    "allowed_paths": ["scripts/reconcile_records.py", "scripts/tests/**"],
    "protected_paths": ["scripts/reconcile_records.py"], "deliverables_new": ["scripts/reconcile_records.py"],
    "watched": ["**/Makefile", "scripts/reconcile_paths.py"],
    "dependencies": [{"packet_id": "PLAN-SDLC-001/MATCHER1", "packet_ref": ATTEMPT + "packet.json",
                      "delivery_ref": ATTEMPT + "delivery.json"}],
    "release_disposition": {topic: {"status": "not-applicable", "statement": STATEMENT} for topic in TOPICS},
}
APPROVAL = {
    "record_type": "flowstate-plan-approval-v1", "packet_id": "PLAN-SDLC-001/RECORDS1",
    "packet_ref": ATTEMPT + "packet.json", "packet_sha256": "cd" * 32, "plan_ref": PLAN, "plan_sha256": "ab" * 32,
    "reviewer": {"actor": "Principal-Engineer", "session": OTHER, "evidence": "persisted session read-back"},
    "author_session": SESSION, "protected_paths_approved": ["scripts/reconcile_records.py"], "verdict": "ACCEPT",
    "findings": [{"id": "F1", "severity": "P3", "status": "resolved"}], "approved_at": "2026-11-26T10:00:00Z",
    "delivered_commit": "", "attestation": {"algorithm": "Ed25519", "key_id": "ef" * 32, "signature": "01" * 64},
}
LOADERS = (("packet", load_packet, PACKET), ("approval", load_approval, APPROVAL))


def raw(record, **kw):
    return json.dumps(record, **kw).encode()


def changed(record, key, value):
    result = copy.deepcopy(record)
    result[key] = value
    return result


class ValidSamplesTest(unittest.TestCase):
    def test_valid_samples_load_unchanged_including_pretty_printed_bytes(self):
        for name, load, record in LOADERS:
            with self.subTest(name=name):
                self.assertEqual((load(raw(record), IDS), load(raw(record, indent=2) + b"\n", IDS)), (record, record))

    def test_delivered_commit_accepts_a_forty_character_commit(self):
        self.assertEqual(load_approval(raw(changed(APPROVAL, "delivered_commit", "a" * 40)), IDS)["delivered_commit"],
                         "a" * 40)

    def test_dependency_on_a_mixed_case_heading_id_loads(self):
        record = changed(PACKET, "dependencies", [dict(PACKET["dependencies"][0], packet_id="PLAN-RUN-001/R1a")])
        self.assertEqual(load_packet(raw(record), IDS)["dependencies"][0]["packet_id"], "PLAN-RUN-001/R1a")

    def test_watched_may_begin_with_a_wildcard_and_lists_may_hold_32_entries(self):
        record = changed(changed(PACKET, "allowed_paths", ["a/f%d" % i for i in range(32)]), "watched", ["*.md"] * 32)
        self.assertEqual(load_packet(raw(record), IDS), record)

    def test_record_of_exactly_128_kib_loads(self):
        data = raw(PACKET) + b" " * (128 * 1024 - len(raw(PACKET)))
        self.assertEqual(load_packet(data, IDS), PACKET)


class ClosedKeysTest(unittest.TestCase):
    def test_extra_key_or_missing_key_is_rejected_naming_it(self):
        for name, load, record in LOADERS:
            with self.subTest(name=name, case="extra"), self.assertRaisesRegex(ReconcileError, "surprise"):
                load(raw(changed(record, "surprise", 1)), IDS)
            for key in record:
                trimmed = {k: v for k, v in record.items() if k != key}
                with self.subTest(name=name, missing=key), self.assertRaisesRegex(ReconcileError, key):
                    load(raw(trimmed), IDS)

    def test_the_other_records_keys_are_not_accepted(self):
        for load, record in ((load_packet, APPROVAL), (load_approval, PACKET)):
            with self.assertRaises(ReconcileError):
                load(raw(record), IDS)

    def test_nested_objects_are_closed(self):
        for load, record, key, value in (
                (load_packet, PACKET, "dependencies", [dict(PACKET["dependencies"][0], extra="x")]),
                (load_approval, APPROVAL, "reviewer", dict(APPROVAL["reviewer"], extra="x")),
                (load_approval, APPROVAL, "findings", [dict(APPROVAL["findings"][0], extra="x")]),
                (load_approval, APPROVAL, "attestation", dict(APPROVAL["attestation"], extra="x"))):
            with self.subTest(key=key), self.assertRaisesRegex(ReconcileError, key):
                load(raw(changed(record, key, value)), IDS)


class MalformedBytesTest(unittest.TestCase):
    def test_duplicate_key_is_rejected_with_its_reason(self):
        for name, load, record in LOADERS:
            text = raw(record).decode().replace('"record_type"', '"record_type": "x", "record_type"', 1)
            with self.subTest(name=name), self.assertRaisesRegex(ReconcileError, "duplicate"):
                load(text.encode(), IDS)

    def test_non_object_root_is_rejected(self):
        for name, load, _ in LOADERS:
            for root in (b"[]", b'"text"', b"null", b"7"):
                with self.subTest(name=name, root=root), self.assertRaisesRegex(ReconcileError, "object"):
                    load(root, IDS)

    def test_control_characters_are_rejected_in_values_and_keys(self):
        for name, load, record in LOADERS:
            for control in ("\u0001", "\n", "\x7f"):
                with self.subTest(name=name, control=repr(control)), self.assertRaisesRegex(ReconcileError, "control"):
                    load(raw(changed(record, "packet_id" if name == "approval" else "title", "a" + control)), IDS)
            with self.subTest(name=name, case="key"), self.assertRaisesRegex(ReconcileError, "control"):
                load(raw(changed(record, "bad\u0002key", 1)), IDS)

    def test_oversize_non_bytes_and_undecodable_input_is_rejected(self):
        for data in (raw(PACKET) + b" " * (128 * 1024), raw(PACKET).decode(), b"\xff\xfe", b'{"a": NaN}', b""):
            with self.subTest(data=data[:20]), self.assertRaises(ReconcileError):
                load_packet(data, IDS)


class PacketFieldsTest(unittest.TestCase):
    def rejected(self, key, value, record=PACKET, load=load_packet, ids=IDS):
        with self.subTest(key=key, value=value), self.assertRaisesRegex(ReconcileError, key):
            load(raw(changed(record, key, value)), ids)

    def test_ungrammatical_allowed_paths_are_rejected(self):
        for path in ("*/x", "**/x", "../x", "/abs/x", "a//b", "a/.git/x", "a b", "", 5, "a/" + "b" * 101):
            self.rejected("allowed_paths", [path])

    def test_ungrammatical_literal_and_watched_entries_are_rejected(self):
        self.rejected("protected_paths", ["scripts/*.py"])
        self.rejected("deliverables_new", ["scripts/**"])
        self.rejected("watched", ["a b"])
        self.rejected("watched", ["a/../b"])

    def test_more_than_32_entries_in_any_list_is_rejected(self):
        for key in ("allowed_paths", "protected_paths", "deliverables_new", "watched"):
            self.rejected(key, ["a/f%d" % i for i in range(33)])
        self.rejected("dependencies", [PACKET["dependencies"][0]] * 33)
        self.rejected("protected_paths_approved", ["a/f%d" % i for i in range(33)], APPROVAL, load_approval)

    def test_packet_id_outside_the_supplied_ids_is_rejected(self):
        for ids in (("PLAN-SDLC-001/OTHER1",), (), ("PLAN-SDLC-001",)):
            self.rejected("packet_id", "PLAN-SDLC-001/RECORDS1", ids=ids)
            self.rejected("packet_id", "PLAN-SDLC-001/RECORDS1", APPROVAL, load_approval, ids)
        self.assertEqual(load_packet(raw(changed(PACKET, "packet_id", "PLAN-SDLC-001")), IDS)["packet_id"],
                         "PLAN-SDLC-001")

    def test_plan_ref_outside_the_plan_grammar_is_rejected(self):
        for ref in (ATTEMPT + "packet.json", "https://x.test/PLAN-SDLC-001-r11-a.md", "",
                    "dropbox:/FlowState/Knowledge/Plans/PLAN-SDLC-001-r11-continuous reconciliation.md",
                    "dropbox:/FlowState/Knowledge/Plans/PLAN-SDLC-001-continuous.md",
                    "dropbox:/FlowState/Knowledge/Plans/plan-SDLC-001-r11-x.md",
                    "dropbox:/FlowState/Knowledge/Plans/PLAN-SDLC-01-r11-x.md",
                    "dropbox:/FlowState/Knowledge/Plans/../PLAN-SDLC-001-r11-x.md",
                    "dropbox:/FlowState/Knowledge/Plans/PLAN-SDLC-001-r11-x.json"):
            self.rejected("plan_ref", ref)
            self.rejected("plan_ref", ref, APPROVAL, load_approval)

    def test_author_session_must_be_a_lowercase_hyphenated_uuid(self):
        for session in ("unverified", "", SESSION.upper(), SESSION.replace("-", ""), SESSION + "0", None):
            self.rejected("author_session", session)
            self.rejected("author_session", session, APPROVAL, load_approval)

    def test_metadata_references_must_be_dropbox_attempt_files(self):
        for ref in (PLAN, "https://x.test/a/b.json", ATTEMPT + "x/y.json", "dropbox:/FlowState/Metadata/ACTIVE.json", 3):
            self.rejected("packet_ref", ref, APPROVAL, load_approval)
            self.rejected("dependencies", [dict(PACKET["dependencies"][0], delivery_ref=ref)])
            self.rejected("dependencies", [dict(PACKET["dependencies"][0], packet_ref=ref)])

    def test_scalar_fields_must_have_their_formats(self):
        for key, value in (("record_type", "flowstate-plan-approval-v1"), ("plan_sha256", "AB" * 32),
                           ("title", ""), ("title", "x" * 201), ("title", 7)):
            self.rejected(key, value)
        self.rejected("dependencies", [dict(PACKET["dependencies"][0], packet_id="records1")])

    def test_release_disposition_defect_is_rejected_by_the_disposition_validator(self):
        with self.assertRaisesRegex(ReconcileError, "disposition"):
            load_packet(raw(changed(PACKET, "release_disposition", {})), IDS)
        with self.assertRaisesRegex(ReconcileError, "semantic_versioning"):
            load_packet(raw(changed(PACKET, "release_disposition", dict(
                PACKET["release_disposition"], semantic_versioning={"status": "not-applicable", "statement": "no"}))), IDS)


class ApprovalFieldsTest(unittest.TestCase):
    def test_malformed_approval_fields_are_rejected_naming_the_key(self):
        reviewer = APPROVAL["reviewer"]
        for key, value in (
                ("reviewer", dict(reviewer, session="unverified")), ("reviewer", dict(reviewer, actor="")),
                ("reviewer", {"actor": "a"}), ("reviewer", "Principal-Engineer"),
                ("findings", [{"id": "F1", "severity": "high", "status": "open"}]),
                ("findings", [{"id": "", "severity": "P1", "status": "open"}]), ("findings", "none"),
                ("verdict", "accept"), ("verdict", ""), ("approved_at", "2026-13-40T10:00:00Z"),
                ("approved_at", "2026-11-26T10:00:00+01:00"), ("approved_at", "yesterday"),
                ("delivered_commit", "abc123"), ("delivered_commit", "A" * 40), ("packet_sha256", "z" * 64),
                ("protected_paths_approved", ["a/*"]), ("protected_paths_approved", "a/b"),
                ("attestation", {}), ("attestation", dict(APPROVAL["attestation"], algorithm="RSA")),
                ("attestation", dict(APPROVAL["attestation"], signature="01" * 63)), ("attestation", "signed")):
            with self.subTest(key=key, value=value), self.assertRaisesRegex(ReconcileError, key):
                load_approval(raw(changed(APPROVAL, key, value)), IDS)

    def test_signature_content_is_not_verified_here(self):
        forged = changed(APPROVAL, "attestation", {"algorithm": "Ed25519", "key_id": "00" * 32, "signature": "00" * 64})
        self.assertEqual(load_approval(raw(forged), IDS), forged)


if __name__ == "__main__":
    unittest.main()
