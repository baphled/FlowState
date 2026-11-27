import hashlib
import json
import socket
import subprocess
import unittest
from unittest import mock
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat
from scripts.reconcile_changes import Change
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_disposition import TOPICS
from scripts.reconcile_paths import WorkExceeded
from scripts.reconcile_protect import MINIMUM
from scripts.reconcile_scope import Live, check_changes, check_dependencies, check_duplicates, check_packet
from scripts.review_signature import canonical_payload

PLAN = "dropbox:/FlowState/Knowledge/Plans/PLAN-SDLC-001-r11-continuous-reconciliation.md"
IDS = ("PLAN-SDLC-001", "PLAN-SDLC-001/SCOPE1", "PLAN-SDLC-001/APPROVAL1")
PLAN_BYTES = b"# PLAN-SDLC-001\n\nSCOPE1 plan text\n"
ATTEMPT = "dropbox:/FlowState/Metadata/attempts/2026-11-26-sdlc-reconcile-approval/"
PACKET_REF, APPROVAL_REF, DELIVERY_REF, ADMISSION_REF = [ATTEMPT + name for name in ("packet.json", "plan-approval.json", "delivery.json", "admission.json")]
TRAILER = "Project-Metadata: " + ADMISSION_REF
AUTHOR, REVIEWER = "01a2c0de-0000-7000-8000-000000000001", "01a2c0de-0000-7000-8000-000000000002"
STATEMENT = "This packet touches nothing in this area, so there is no behaviour or record for the topic to constrain."
PATHS = ["scripts/reconcile_scope.py", "scripts/reconcile_protect.py", "scripts/tests/test_reconcile_scope.py", "scripts/tests/test_reconcile_protect.py"]
COMMIT, TIP, ADVANCED = "c" * 40, "a" * 40, "b" * 40
PRIVATE, OTHER = Ed25519PrivateKey.generate(), Ed25519PrivateKey.generate()
PUBLIC = PRIVATE.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)


def raw(record):
    return json.dumps(record).encode()


def packet(packet_id, paths, **extra):
    """A valid work-packet record owning `paths` (allowed, protected and new), with nothing applicable to release."""
    record = {"record_type": "flowstate-work-packet-v1", "packet_id": packet_id, "title": "Scope checks",
              "plan_ref": PLAN, "plan_sha256": hashlib.sha256(PLAN_BYTES).hexdigest(), "author_session": AUTHOR,
              "allowed_paths": paths, "protected_paths": paths, "deliverables_new": paths, "watched": [], "dependencies": [],
              "release_disposition": {topic: {"status": "not-applicable", "statement": STATEMENT} for topic in TOPICS}}
    return dict(record, **extra)


def approval(key=PRIVATE, **extra):
    """Signed receipt bytes for `DEP`: the canonical receipt with `extra` applied, signed with `key`."""
    public = key.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)
    receipt = {"record_type": "flowstate-plan-approval-v1", "packet_id": DEP["packet_id"], "packet_ref": PACKET_REF,
               "packet_sha256": DEP_DIGEST, "plan_ref": PLAN, "plan_sha256": DEP["plan_sha256"],
               "reviewer": {"actor": "QA-Engineer", "session": REVIEWER, "evidence": "persisted session read-back"},
               "author_session": AUTHOR, "protected_paths_approved": DEP["protected_paths"], "verdict": "ACCEPT",
               "findings": [], "approved_at": "2026-11-26T10:00:00Z", "delivered_commit": "", **extra}
    return raw(dict(receipt, attestation={"algorithm": "Ed25519", "key_id": hashlib.sha256(public).hexdigest(),
                                          "signature": key.sign(canonical_payload(receipt)).hex()}))


def delivery(**extra):
    """A delivery note for `DEP` naming `COMMIT`."""
    return raw({"record_type": "flowstate-packet-delivery-v1", "packet_id": DEP["packet_id"],
                "packet_sha256": DEP_DIGEST, "commit": COMMIT, **extra})


DEP = packet("PLAN-SDLC-001/APPROVAL1", ["scripts/reconcile_approval.py"])
DEP_BYTES, DEP_DIGEST = raw(DEP), hashlib.sha256(raw(DEP)).hexdigest()
STORED = {PACKET_REF: DEP_BYTES, APPROVAL_REF: approval(), DELIVERY_REF: delivery(), ADMISSION_REF: raw({"packet_sha256": DEP_DIGEST})}


class World:
    """Stored bytes, commit facts and the target tree behind the `read` and `source` calls; what is absent raises."""

    def __init__(self, files=None, message=TRAILER, ancestry=None, tree=None):
        self.files, self.message, self.tree = dict(STORED, **(files or {})), message, tree
        self.ancestry = {TIP: (COMMIT,), ADVANCED: (COMMIT,)} if ancestry is None else ancestry

    def answer(self, value):
        """A stored fixture value, or the exception it stands for, raised."""
        if isinstance(value, Exception):
            raise value
        return value

    def read(self, reference):
        return self.answer(self.files[reference])

    def commit_message(self, commit):
        return self.answer(self.message)

    def is_ancestor(self, commit, tip):
        return commit in self.ancestry[tip]

    def tree_entries(self, commit):
        return self.answer(self.tree) or {}


def live(tip=TIP, **changes):
    """A `Live` over a `World` whose defaults satisfy the dependency rule for `COMMIT` reachable from `tip`."""
    world = World(**changes)
    return Live(tip, world, world.read, PLAN_BYTES, PUBLIC, IDS)


PACKET = packet("PLAN-SDLC-001/SCOPE1", PATHS)
RECEIPT = {"protected_paths_approved": PATHS}
DECIDER = packet("PLAN-SDLC-001/SCOPE1", PATHS, dependencies=[{"packet_id": DEP["packet_id"], "packet_ref": PACKET_REF, "delivery_ref": DELIVERY_REF}])
ADDED = [Change(path, "A", "100644", "000000") for path in PATHS]


def scope(changes, packet=PACKET, receipt=RECEIPT, configured=MINIMUM):
    return check_changes(packet, receipt, changes, configured)


class ChangedPathsTest(unittest.TestCase):
    def test_paths_inside_the_allowlist_are_allowed(self):
        self.assertIsNone(scope(ADDED))
        self.assertIsNone(scope([]))

    def test_a_path_outside_the_allowlist_is_blocked(self):
        for path in ("docs/other.md", "Makefile", "scripts/reconcile_other.py", "scripts/tests/test_x.py", "../escape"):
            with self.subTest(path=path), self.assertRaisesRegex(ReconcileError, "outside allowed_paths"):
                scope(ADDED + [Change(path, "A", "100644", "000000")])

    def test_a_wildcard_allowlist_still_needs_every_protected_path_listed(self):
        record = packet("PLAN-SDLC-001/SCOPE1", ["scripts/tests/**"], protected_paths=["scripts/tests/test_reconcile_x.py"])
        changes = [Change("scripts/tests/" + name, "A", "100644", "000000") for name in ("test_x.py", "test_reconcile_x.py")]
        self.assertIsNone(scope(changes, record, {"protected_paths_approved": ["scripts/tests/test_reconcile_x.py"]}))
        with self.assertRaisesRegex(ReconcileError, "protected path absent"):
            scope(changes, record, {"protected_paths_approved": ["scripts/tests/test_x.py"]})

    def test_a_protected_path_absent_from_the_packet_or_the_receipt_is_blocked(self):
        unlisted = packet("PLAN-SDLC-001/SCOPE1", PATHS, protected_paths=[])
        for record, receipt in ((PACKET, {"protected_paths_approved": []}), (unlisted, RECEIPT)):
            with self.subTest(receipt=receipt), self.assertRaisesRegex(ReconcileError, "protected path absent"):
                scope(ADDED, record, receipt)

    def test_symlink_and_gitlink_changes_are_never_allowed(self):
        for modes in (("120000", "000000"), ("100644", "120000"), ("160000", "160000")):
            with self.subTest(modes=modes), self.assertRaisesRegex(ReconcileError, "symlink or gitlink"):
                scope([Change(PATHS[0], "M", *modes)])

    def test_a_configuration_that_drops_a_minimum_entry_is_blocked(self):
        with self.assertRaisesRegex(ReconcileError, "minimum"):
            scope([], configured=MINIMUM[1:])

    def test_an_exhausted_matcher_budget_blocks_instead_of_passing(self):
        with mock.patch("scripts.reconcile_scope.match", side_effect=WorkExceeded("budget")), self.assertRaisesRegex(ReconcileError, "WorkExceeded"):
            scope(ADDED)


class DependencyTest(unittest.TestCase):
    def blocked(self, pattern, **changes):
        with self.assertRaisesRegex(ReconcileError, pattern):
            check_dependencies(DECIDER, live(**changes))

    def test_a_delivered_dependency_with_a_verified_approval_is_satisfied_even_after_a_target_advance(self):
        self.assertIsNone(check_dependencies(DECIDER, live()))
        self.assertIsNone(check_dependencies(DECIDER, live(tip=ADVANCED)))
        self.assertIsNone(check_dependencies(PACKET, live()))

    def test_an_unverified_approval_or_a_malformed_stored_record_leaves_the_dependency_unsatisfied(self):
        reject = raw(dict(json.loads(approval()), verdict="REJECT"))
        for reference, bad in ((APPROVAL_REF, approval(key=OTHER)), (APPROVAL_REF, reject), (APPROVAL_REF, KeyError(APPROVAL_REF)),
                               (PACKET_REF, b"{}"), (DELIVERY_REF, delivery(packet_id="PLAN-SDLC-001/DISPOSITION1")),
                               (DELIVERY_REF, delivery(commit="abc")), (DELIVERY_REF, delivery(extra=1)), (DELIVERY_REF, b"[]")):
            with self.subTest(bad=bad), self.assertRaises(ReconcileError):
                check_dependencies(DECIDER, live(files={reference: bad}))

    def test_a_delivery_note_naming_an_unreachable_commit_is_blocked(self):
        self.blocked("reachable", ancestry={TIP: ()})
        self.blocked("reachable", tip=ADVANCED, ancestry={ADVANCED: ()})

    def test_a_delivery_note_whose_digest_differs_from_the_approved_packet_is_blocked(self):
        self.blocked("digest", files={DELIVERY_REF: delivery(packet_sha256="ab" * 32)})

    def test_a_delivery_note_outside_the_dependency_directory_is_blocked(self):
        entry = dict(DECIDER["dependencies"][0], delivery_ref=ATTEMPT.replace("approval", "changes") + "delivery.json")
        with self.assertRaisesRegex(ReconcileError, "directory"):
            check_dependencies(packet("PLAN-SDLC-001/SCOPE1", PATHS, dependencies=[entry]), live())

    def test_a_commit_whose_trailer_resolves_elsewhere_or_is_not_single_is_blocked(self):
        foreign_ref = ADMISSION_REF.replace("approval", "changes")
        foreign_trailer = "Project-Metadata: " + foreign_ref
        # Store the foreign admission record so the directory check, not a KeyError, is what refuses.
        foreign_world = {**STORED, foreign_ref: raw({"packet_sha256": DEP_DIGEST})}
        # A single foreign trailer is refused by the directory check.
        world = World(message=foreign_trailer, files=foreign_world)
        with self.assertRaisesRegex(ReconcileError, "resolves elsewhere"):
            check_dependencies(DECIDER, Live(TIP, world, world.read, PLAN_BYTES, PUBLIC, IDS))
        # Two trailers or no trailer are refused by the reference extraction.
        for message in (TRAILER + "\n" + foreign_trailer, "feat(build): deliver without a trailer"):
            with self.subTest(message=message), self.assertRaisesRegex(ReconcileError, "trailer"):
                check_dependencies(DECIDER, live(message=message))
        # The same foreign trailer is refused even when the approval names the delivered commit,
        # because the directory check is the only defence when the admission record is never read.
        world = World(message=foreign_trailer, files={**foreign_world, APPROVAL_REF: approval(delivered_commit=COMMIT)})
        with self.assertRaisesRegex(ReconcileError, "resolves elsewhere"):
            check_dependencies(DECIDER, Live(TIP, world, world.read, PLAN_BYTES, PUBLIC, IDS))

    def test_the_commit_is_bound_by_its_admission_record_or_by_the_approval_naming_it(self):
        unbound = raw({"packet_sha256": "ab" * 32})
        for admission in (unbound, b"[]", b"{", KeyError(ADMISSION_REF)):
            with self.subTest(admission=admission), self.assertRaises(ReconcileError):
                check_dependencies(DECIDER, live(files={ADMISSION_REF: admission}))
        named = approval(delivered_commit=COMMIT)
        self.assertIsNone(check_dependencies(DECIDER, live(files={APPROVAL_REF: named, ADMISSION_REF: unbound})))
        self.assertIsNone(check_dependencies(DECIDER, live(files={APPROVAL_REF: named, ADMISSION_REF: KeyError(ADMISSION_REF)})))

    def test_any_other_lookup_error_blocks(self):
        failures = [{"files": {reference: OSError("unreadable")}} for reference in (PACKET_REF, APPROVAL_REF, DELIVERY_REF)]
        failures.append({"ancestry": {TIP: OSError("down")}})
        for changes in failures:
            with self.subTest(changes=changes), self.assertRaises(ReconcileError):
                check_dependencies(DECIDER, live(**changes))


class DuplicateWorkTest(unittest.TestCase):
    def test_a_deliverable_absent_from_the_live_target_is_accepted(self):
        self.assertIsNone(check_duplicates(PATHS, live(tree={"scripts": "040000", "scripts/other.py": "100644"})))

    def test_a_deliverable_already_on_the_target_is_blocked(self):
        with self.assertRaisesRegex(ReconcileError, "scripts/reconcile_protect.py"):
            check_duplicates(PATHS, live(tree={"scripts/reconcile_protect.py": "100644"}))

    def test_any_tree_lookup_error_blocks(self):
        for error in (ReconcileError("tree listing is truncated"), OSError("down")):
            with self.subTest(error=error), self.assertRaises(ReconcileError):
                check_duplicates(PATHS, live(tree=error))


class CheckPacketTest(unittest.TestCase):
    def test_a_candidate_inside_the_packet_passes_every_check(self):
        self.assertIsNone(check_packet(DECIDER, RECEIPT, ADDED, MINIMUM, live(), before_merge=True))
        self.assertIsNone(check_packet(PACKET, RECEIPT, ADDED, MINIMUM, live()))

    def test_duplicate_work_is_checked_before_merge_only(self):
        world = live(tree={"scripts/reconcile_scope.py": "100644"})
        self.assertIsNone(check_packet(PACKET, RECEIPT, ADDED, MINIMUM, world))
        with self.assertRaisesRegex(ReconcileError, "live target"):
            check_packet(PACKET, RECEIPT, ADDED, MINIMUM, world, before_merge=True)

    def test_a_missing_or_malformed_disposition_blocks(self):
        for disposition in ({}, dict(PACKET["release_disposition"], semantic_versioning={"status": "applicable"})):
            with self.subTest(disposition=disposition), self.assertRaisesRegex(ReconcileError, "disposition"):
                check_packet(packet("PLAN-SDLC-001/SCOPE1", PATHS, release_disposition=disposition), RECEIPT, [], MINIMUM, live())

    def test_an_out_of_allowlist_change_makes_check_packet_refuse(self):
        rogue = [Change("internal/app/rogue.go", "A", "100644", "000000")]
        with self.assertRaisesRegex(ReconcileError, "scope"):
            check_packet(DECIDER, RECEIPT, ADDED + rogue, MINIMUM, live(), before_merge=True)

    def test_an_unsatisfied_dependency_makes_check_packet_refuse(self):
        unreachable = delivery(commit="f" * 40)
        with self.assertRaisesRegex(ReconcileError, "dependency"):
            check_packet(DECIDER, RECEIPT, ADDED, MINIMUM, live(files={DELIVERY_REF: unreachable}))

    def test_scope_precedes_dependency_when_both_defects_are_present(self):
        rogue = [Change("internal/app/rogue.go", "A", "100644", "000000")]
        unreachable = delivery(commit="f" * 40)
        with self.assertRaisesRegex(ReconcileError, "scope"):
            check_packet(DECIDER, RECEIPT, ADDED + rogue, MINIMUM,
                         live(files={DELIVERY_REF: unreachable}), before_merge=True)

    def test_a_release_sensitive_packet_needs_one_applicable_topic(self):
        record = packet("PLAN-SDLC-001/SCOPE1", ["go.mod"], protected_paths=["go.mod"], deliverables_new=["go.mod"])
        approved, edit = {"protected_paths_approved": ["go.mod"]}, [Change("go.mod", "M", "100644", "100644")]
        with self.assertRaisesRegex(ReconcileError, "release_sensitive_paths"):
            check_packet(record, approved, edit, MINIMUM, live(), ("go.mod",))
        self.assertIsNone(check_packet(record, approved, edit, MINIMUM, live()))

    def test_no_connection_or_command_is_opened_by_the_module(self):
        with mock.patch.object(socket, "socket", side_effect=AssertionError("connection")), \
                mock.patch("subprocess.run", side_effect=AssertionError("command")), \
                mock.patch("subprocess.check_output", side_effect=AssertionError("command")):
            self.assertIsNone(check_packet(DECIDER, RECEIPT, ADDED, MINIMUM, live(), before_merge=True))


if __name__ == "__main__":
    unittest.main()
