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

# The dependency is approved under the previous plan revision; the current one no longer restates its packet heading.
PLAN, CANDIDATE_PLAN = ["dropbox:/FlowState/Knowledge/Plans/PLAN-SDLC-001-r%d-continuous-reconciliation.md" % revision
                        for revision in (11, 12)]
IDS = ("PLAN-SDLC-001", "PLAN-SDLC-001/SCOPE1")
DROPBOX = (["Metadata/**"], ["Metadata/attempts/2026-11-26-sdlc-reconcile-approval/summary.md"])
TREE = {"scripts": "040000", "scripts/reconcile_scope.py": "100644", "Makefile": "100644", "docs": "040000"}
ATTEMPT = "dropbox:/FlowState/Metadata/attempts/2026-11-26-sdlc-reconcile-approval/"
PACKET_REF, APPROVAL_REF, DELIVERY_REF, ADMISSION_REF = [ATTEMPT + name for name in ("packet.json", "plan-approval.json", "delivery.json", "admission.json")]
TRAILER = "Project-Metadata: " + ADMISSION_REF
AUTHOR, REVIEWER = "01a2c0de-0000-7000-8000-000000000001", "01a2c0de-0000-7000-8000-000000000002"
STATEMENT = "This packet touches nothing in this area, so there is no behaviour or record for the topic to constrain."
PATHS = ["scripts/reconcile_scope.py", "scripts/reconcile_protect.py", "scripts/tests/test_reconcile_scope.py", "scripts/tests/test_reconcile_protect.py"]
COMMIT, TIP, ADVANCED = "c" * 40, "a" * 40, "b" * 40
PRIVATE, OTHER = Ed25519PrivateKey.generate(), Ed25519PrivateKey.generate()
PUBLIC = PRIVATE.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)


def plan_bytes(text, headings="### SCOPE1 \u2014 Scope\n\n### APPROVAL1 \u2014 Approval\n\n"):
    """Plan bytes the gate's id parser accepts: a front-matter id, then a packet section holding `headings`."""
    return ("---\nid: PLAN-SDLC-001\n---\n\n## 9. Delivery packets\n\n" + headings + "## 10. Acceptance\n\n"
            + text).encode()


PLAN_BYTES = plan_bytes("r11 plan text\n")
CANDIDATE_BYTES = plan_bytes("r12 plan text\n", "### SCOPE1 \u2014 Scope\n\n")


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


def undelivered(paths=DROPBOX, key=PRIVATE, **signed):
    """Stored bytes of a dependency whose delivery note carries no commit: a packet declaring `paths` as its allowed and
    new paths, the approval `key` signed for exactly those bytes with `signed` applied, and a delivery note with no commit."""
    record = packet("PLAN-SDLC-001/APPROVAL1", paths[0], protected_paths=[], deliverables_new=paths[1])
    digest = hashlib.sha256(raw(record)).hexdigest()
    return {PACKET_REF: raw(record),
            APPROVAL_REF: approval(key=key, **dict([("packet_sha256", digest), ("protected_paths_approved", [])],
                                               **signed)),
            DELIVERY_REF: delivery(packet_sha256=digest, commit="")}


DEP = packet("PLAN-SDLC-001/APPROVAL1", ["scripts/reconcile_approval.py"])
DEP_BYTES, DEP_DIGEST = raw(DEP), hashlib.sha256(raw(DEP)).hexdigest()
STORED = {PLAN: PLAN_BYTES, CANDIDATE_PLAN: CANDIDATE_BYTES, PACKET_REF: DEP_BYTES, APPROVAL_REF: approval(),
          DELIVERY_REF: delivery(), ADMISSION_REF: raw({"packet_sha256": DEP_DIGEST})}


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
    """A `Live` over a `World` whose defaults satisfy the dependency rule for `COMMIT` reachable from `tip`; the plan bytes
    it carries are the candidate's, so a dependency is never verified against them."""
    world = World(**changes)
    return Live(tip, world, world.read, CANDIDATE_BYTES, PUBLIC, IDS)


def candidate(packet_id, **extra):
    """A work-packet record of the candidate: it owns `PATHS` and is approved under the current plan revision."""
    return packet(packet_id, PATHS, plan_ref=CANDIDATE_PLAN, plan_sha256=hashlib.sha256(CANDIDATE_BYTES).hexdigest(), **extra)


PACKET = candidate("PLAN-SDLC-001/SCOPE1")
RECEIPT = {"protected_paths_approved": PATHS}
DEPENDENCY = {"packet_id": DEP["packet_id"], "packet_ref": PACKET_REF, "delivery_ref": DELIVERY_REF}
DECIDER = candidate("PLAN-SDLC-001/SCOPE1", dependencies=[DEPENDENCY])
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

    def test_a_dependency_approved_under_an_earlier_plan_revision_is_verified_against_that_revision(self):
        """`DEP` is approved under r11 and the candidate under r12, which no longer lists it: the bytes and ids at the
        dependency's own `plan_ref` decide, never the candidate's."""
        self.assertIsNone(check_dependencies(DECIDER, live(files={CANDIDATE_PLAN: KeyError(CANDIDATE_PLAN)})))
        later = approval(plan_ref=CANDIDATE_PLAN, plan_sha256=hashlib.sha256(CANDIDATE_BYTES).hexdigest())
        rows = (("approval bound to the candidate's revision", {APPROVAL_REF: later}, "plan digest"),
                ("plan without ids", {PLAN: b"# no front-matter id or packet section\n"}, "no front-matter id"),
                ("plan holding the candidate's bytes", {PLAN: CANDIDATE_BYTES}, "packet_id is invalid"))
        for name, stored, refusal in rows:
            with self.subTest(name=name), self.assertRaisesRegex(ReconcileError, refusal):
                check_dependencies(DECIDER, live(files=stored))

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
        unreadable = {reference: OSError("unreadable") for reference in (PACKET_REF, PLAN, APPROVAL_REF, DELIVERY_REF)}
        failures = [{"files": {reference: error}} for reference, error in unreadable.items()]
        failures.append({"ancestry": {TIP: OSError("down")}})
        for changes in failures:
            with self.subTest(changes=changes), self.assertRaises(ReconcileError):
                check_dependencies(DECIDER, live(**changes))


class CommitlessDependencyTest(unittest.TestCase):
    """Section 6: a delivery note with an empty commit is satisfied by its verified approval alone, only when that approval
    names no delivered commit and the dependency declares nothing in this repository."""

    def ruled(self, files, tree=None, refusal=None):
        """Run the dependency rule over stored `files` and the target `tree`: satisfied, or refused naming `refusal`."""
        if refusal is None:
            self.assertIsNone(check_dependencies(DECIDER, live(files=files, tree=tree)))
        else:
            with self.assertRaisesRegex(ReconcileError, refusal):
                check_dependencies(DECIDER, live(files=files, tree=tree))

    def test_no_commit_no_delivered_commit_and_nothing_in_the_repository_is_satisfied(self):
        for tree in (None, TREE):
            with self.subTest(tree=tree):
                self.ruled(undelivered(paths=([], [])), tree)

    def test_a_deliverable_under_a_dropbox_path_is_satisfied_against_the_repository_tree(self):
        self.ruled(undelivered(), TREE)

    def test_an_approval_naming_a_commit_refuses_a_delivery_note_that_carries_none(self):
        for named in (COMMIT, "f" * 40):
            with self.subTest(named=named):
                self.ruled(undelivered(delivered_commit=named), None, "approval names one")

    def test_a_path_in_the_repository_is_refused_with_or_without_the_file_in_the_tree(self):
        """A deliverable in the tree, one under a top-level directory such as `scripts` or `docs` without the file in the
        tree, one named only by `deliverables_new`, and an all-new-files code packet whose record omits its new
        paths from `deliverables_new` are all refused."""
        in_tree = (["scripts/reconcile_approval.py"], ["scripts/reconcile_approval.py"])
        for paths, tree in ((in_tree, {"scripts": "040000", "scripts/reconcile_approval.py": "100644"}),
                            ((["docs/guide.md"], ["docs/guide.md"]), {"docs": "040000"}),
                            ((DROPBOX[0], ["docs/guide.md"]), {"docs": "040000"}),
                            ((["internal/app/*.go"], []), {"internal": "040000"}),
                            ((in_tree[0], []), {"scripts/reconcile_approval.py": "100644"})):
            with self.subTest(paths=paths, tree=tree):
                self.ruled(undelivered(paths), tree, "declares")

    def test_the_approval_alone_carries_a_commit_less_delivery_so_its_defects_are_refused(self):
        """An unverified approval, one for another digest, a missing approval, a delivery note for another packet and a
        missing delivery note each leave the dependency unsatisfied."""
        for name, stored in (("unverified", undelivered(key=OTHER)),
                             ("another digest", undelivered(packet_sha256="ab" * 32)),
                             ("no approval", {APPROVAL_REF: KeyError(APPROVAL_REF)}),
                             ("note for another packet", {DELIVERY_REF: delivery(commit="")}),
                             ("no delivery note", {DELIVERY_REF: KeyError(DELIVERY_REF)})):
            with self.subTest(name=name), self.assertRaises(ReconcileError):
                check_dependencies(DECIDER, live(files={**undelivered(), **stored}))


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
