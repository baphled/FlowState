"""Owning tests for scripts/reconcile_gate.py: the decision order on injected fixtures and the real command line."""
import hashlib
import itertools
import json
import os
import re
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timezone
from pathlib import Path
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat
from scripts.reconcile_changes import Change
from scripts.reconcile_config import CONTEXTS, ReconcileError, parse_config
from scripts.reconcile_disposition import TOPICS
from scripts.reconcile_evidence import canonical_bytes, load, seal
from scripts.reconcile_gate import ALLOWED, TABLE, World, decide
from scripts.reconcile_protect import MINIMUM
from scripts.review_signature import canonical_payload

ROOT = Path(__file__).resolve().parents[2]
NOW = datetime(2026, 11, 27, 12, 0, 0, tzinfo=timezone.utc)
TIP, MOVED, HEAD, STRANGER = "a" * 40, "b" * 40, "c" * 40, "d" * 40
PACKET, PATH = "PLAN-X-001/GATE1", "docs/gate.md"
ATTEMPT = "dropbox:/FlowState/Metadata/attempts/2026-11-27-gate/"
ADMISSION, PACKET_REF, APPROVAL_REF, ACTIVATION_REF = [ATTEMPT + name + ".json" for name in ("admission", "packet", "plan-approval", "activation")]
PLAN_REF = "dropbox:/FlowState/Knowledge/Plans/PLAN-X-001-r1-gate.md"
PLAN_BYTES = "---\nid: PLAN-X-001\n---\n\n## 9. Delivery packets\n\n### GATE1 \u2014 Gate\n\n### OTHER1 \u2014 Other\n\n## 10. Verification\n".encode()
AUTHOR, REVIEWER = "01a217ce-07d3-738b-9863-28a360afef28", "01a21a01-9dc3-7036-8939-65f53321682d"
STATEMENT = "This packet touches nothing in this area, so there is no behaviour or record for the topic to constrain."
KEY, OTHER = Ed25519PrivateKey.generate(), Ed25519PrivateKey.generate()


def sha(raw_bytes):
    return hashlib.sha256(raw_bytes).hexdigest()


def public(key):
    return key.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)


def raw(value):
    return json.dumps(value).encode()


def signed(record, key=KEY):
    """The record with an attestation signed over its canonical payload, as the signer issues it."""
    attestation = {"algorithm": "Ed25519", "key_id": sha(public(key)), "signature": key.sign(canonical_payload(record)).hex()}
    return raw(dict(record, attestation=attestation))


def settings(url="https://example.test/acme/widget.git"):
    """A configuration document: every context resolves through git only, so no network or token is needed."""
    return {"repo": "acme/widget", "remote": {"name": "origin", "url": url}, "integration_branch": "dev", "release_branch": "stable",
            "resolve_modes": {context: ["ls-remote"] for context in CONTEXTS}, "plan_approval_public_key": public(KEY).hex(),
            "activation_ref": ACTIVATION_REF, "protected_globs": list(MINIMUM), "system_one_model": "", "release_sensitive_paths": []}


def config(**changes):
    return parse_config(dict(settings(), **changes))


PACKET_RECORD = {"record_type": "flowstate-work-packet-v1", "packet_id": PACKET, "title": "Gate", "plan_ref": PLAN_REF,
                 "plan_sha256": sha(PLAN_BYTES), "author_session": AUTHOR, "allowed_paths": [PATH], "protected_paths": [],
                 "deliverables_new": [PATH], "watched": ["Makefile"], "dependencies": [],
                 "release_disposition": {topic: {"status": "not-applicable", "statement": STATEMENT} for topic in TOPICS}}
RECEIPT = {"record_type": "flowstate-plan-approval-v1", "packet_id": PACKET, "packet_ref": PACKET_REF,
           "packet_sha256": sha(raw(PACKET_RECORD)), "plan_ref": PLAN_REF, "plan_sha256": sha(PLAN_BYTES),
           "reviewer": {"actor": "QA-Engineer", "session": REVIEWER, "evidence": "persisted session read-back"},
           "author_session": AUTHOR, "protected_paths_approved": [], "verdict": "ACCEPT", "findings": [],
           "approved_at": "2026-11-26T10:00:00Z", "delivered_commit": ""}
ADMISSION_RECORD = {"record_type": "flowstate-project-metadata-v1", "packet_id": PACKET, "packet_ref": PACKET_REF,
                    "packet_sha256": RECEIPT["packet_sha256"], "approval_ref": APPROVAL_REF}
FILES = {ADMISSION: raw(ADMISSION_RECORD), PACKET_REF: raw(PACKET_RECORD), PLAN_REF: PLAN_BYTES, APPROVAL_REF: signed(RECEIPT)}


class Fake:
    """Transport, change source and Dropbox tree in one fixture: `tips` is read once per resolution and repeats the
    tip unless given, `diffs` maps a (base, head) range to its changed paths, `tree` is the target tree and `files`
    maps a Dropbox reference to bytes or to the error it raises."""

    def __init__(self, tips=None, diffs=None, tree=None, message="candidate\n\nProject-Metadata: " + ADMISSION, **files):
        self.tips = iter(tips) if tips else itertools.repeat(TIP)
        self.diffs, self.tree, self.message, self.ref, self.reads = diffs or {}, tree or {}, message, None, []
        self.files = dict(FILES, **files)

    def commit_message(self, commit):
        return self.message

    def tree_entries(self, commit):
        return self.tree

    def ls_remote(self, url, ref):
        self.ref = ref
        return next(self.tips)

    def read(self, reference):
        self.reads.append(reference)
        if isinstance(self.files[reference], Exception):
            raise self.files[reference]
        return self.files[reference]

    def changed_paths(self, base, head):
        return [Change(path, "M", "100644", "100644") for path in self.diffs.get((base, head), [])]


class DecisionTest(unittest.TestCase):
    """`decide` on injected fixtures; the exit code is the one the command line returns."""

    def gate(self, fake, checkpoint="pre-push", cache=None, context=None, candidate=HEAD, **options):
        """(exit code, record or refusal) for one evaluation of `fake`."""
        world = World(config(**options), checkpoint, PACKET, candidate, fake, fake.read, cache, lambda: NOW, fake)
        try:
            record, _ = decide(world, context)
        except ReconcileError as error:
            return 1, error
        return int(record is not None and record["outcome"] not in ALLOWED), record

    def test_a_validly_signed_activation_record_exempts_a_listed_head_from_binding_and_approval_only(self):
        """The commit carries no binding and nothing holds an approval: only a valid record that lists the head allows."""
        listed = {"record_type": "flowstate-activation-v1", "exempt": [{"pr_number": 7, "head_sha": HEAD}], "issued_at": "2026-11-27T11:00:00Z"}
        cases = {"signed and listed": (signed(listed), 0), "unsigned": (raw(dict(listed, attestation={})), 1),
                 "another key": (signed(listed, OTHER), 1), "future issued_at": (signed(dict(listed, issued_at="2026-11-27T12:00:01Z")), 1),
                 "not rfc 3339": (signed(dict(listed, issued_at="2026-11-27")), 1),
                 "another head": (signed(dict(listed, exempt=[{"pr_number": 7, "head_sha": STRANGER}])), 1),
                 "extra key": (signed(dict(listed, note="x")), 1), "malformed entry": (signed(dict(listed, exempt=[{"head_sha": HEAD}])), 1)}
        for name, (record, code) in cases.items():
            with self.subTest(name=name):
                fake = Fake(message="feat: no trailer", **{ACTIVATION_REF: record})
                self.assertEqual((self.gate(fake)[0], fake.reads[0]), (code, ACTIVATION_REF))
        self.assertEqual(self.gate(Fake(message="feat: no trailer"), activation_ref="")[0], 1)

    def test_any_other_head_needs_its_binding_approval_and_scope(self):
        other = raw(dict(PACKET_RECORD, packet_id="PLAN-X-001/OTHER1"))  # validly approved, but not the packet asked for
        cases = {"no trailer": dict(message="feat: no trailer"), "another packet": {ADMISSION: raw(dict(ADMISSION_RECORD, packet_id="PLAN-X-001/OTHER"))},
                 "another stored packet": {PACKET_REF: other, ADMISSION: raw(dict(ADMISSION_RECORD, packet_sha256=sha(other))),
                                           APPROVAL_REF: signed(dict(RECEIPT, packet_id="PLAN-X-001/OTHER1", packet_sha256=sha(other)))},
                 "changed packet bytes": {PACKET_REF: raw(dict(PACKET_RECORD, title="Other"))}, "no approval": {APPROVAL_REF: b"{}"},
                 "rejected verdict": {APPROVAL_REF: signed(dict(RECEIPT, verdict="REJECT"))},
                 "out of scope change": dict(diffs={(TIP, HEAD): ["internal/app/rogue.go"]}),
                 "protected path unapproved": dict(diffs={(TIP, HEAD): ["docs/gate.md", "Makefile"]}),
                 "unreadable plan": {PLAN_REF: OSError("down")}}
        for name, changes in cases.items():
            with self.subTest(name=name):
                self.assertEqual(self.gate(Fake(**changes))[0], 1)
        self.assertEqual(self.gate(Fake())[0], 0)
        fake = Fake(tree={PATH: "100644"})  # the deliverable is already on the live target
        self.assertEqual([self.gate(fake, checkpoint=name)[0] for name in ("pre-merge", "preflight", "pre-push", "closeout")], [1, 1, 0, 0])

    def test_the_ci_context_and_the_release_checkpoints_run_no_dropbox_backed_check(self):
        fake = Fake(message="feat: no trailer")
        self.assertEqual((self.gate(fake, context="ci")[0], fake.reads, fake.ref), (0, [], "refs/heads/dev"))
        for checkpoint in ("release-prepare", "release-publish", "release-verify"):
            with self.subTest(checkpoint=checkpoint):
                self.assertEqual((self.gate(fake, checkpoint)[0], fake.reads, fake.ref), (0, [], "refs/heads/stable"))
        self.assertEqual({context for context, _, _, _ in TABLE.values()}, {"hook", "merge_script", "runner"})
        world = World(config(), "bogus", PACKET, HEAD, fake, fake.read, None, lambda: NOW, fake)
        with self.assertRaises(ReconcileError):  # outside the command line an unknown checkpoint is a refusal
            decide(world)

    def test_a_sha_that_moves_between_resolutions_gets_one_bounded_retry(self):
        self.assertEqual(self.gate(Fake(tips=[TIP, MOVED, TIP, TIP]))[0], 0)
        self.assertIn("moved", str(self.gate(Fake(tips=[TIP, MOVED, TIP, MOVED]))[1]))
        self.assertIn("40-character", str(self.gate(Fake(), candidate="abc")[1]))


class SourceTest(unittest.TestCase):
    def test_no_gate_module_imports_the_advisor(self):
        """Every module the gate reaches through its `scripts.` imports is free of the advisor."""
        pending, seen = {"reconcile_gate.py"}, set()
        while pending - seen:
            path = (pending - seen).pop()
            seen.add(path)
            imported = re.findall(r"(?:from|import) (scripts\.[\w.]+)", (ROOT / "scripts" / path).read_text())
            self.assertNotIn("scripts.reconcile_advisor", imported, path)
            pending.update(name.split(".")[1] + ".py" for name in imported)
        self.assertLessEqual({"reconcile_scope.py", "reconcile_evidence.py", "reconcile_classify.py", "admission_local_dropbox.py"}, seen)


def git(*args, cwd):
    """Run git in `cwd` under a fixed identity; return its output."""
    ident = {"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"}
    done = subprocess.run(("git", "-c", "commit.gpgsign=false", *args), cwd=cwd, env=dict(os.environ, **ident), check=True,
                          capture_output=True, text=True)
    return done.stdout.strip()


class EntryTest(unittest.TestCase):
    """The real command line against a local bare remote and an owner-private Dropbox tree."""

    def setUp(self):
        self.root = self.enterContext(tempfile.TemporaryDirectory())
        self.home, self.work, self.bare = [os.path.join(self.root, name) for name in ("home", "work", "remote.git")]
        self.config, self.private = os.path.join(self.root, "reconcile.json"), os.path.join(self.home, ".config/flowstate/admission.json")
        dropbox = os.path.join(self.root, "dropbox")
        for reference, value in FILES.items():
            path = Path(dropbox + reference.removeprefix("dropbox:"))  # concatenated: never an absolute path
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(value)
        os.chmod(dropbox, 0o700)
        Path(self.private).parent.mkdir(parents=True)
        Path(self.private).write_text(json.dumps({"dropbox_local_path": dropbox}))
        Path(self.config).write_text(json.dumps(settings("file://" + self.bare)))
        git("init", "--bare", self.bare, cwd=self.root)
        git("init", self.work, cwd=self.root)
        git("commit", "--allow-empty", "-m", "target", cwd=self.work)
        git("push", "--quiet", self.bare, "HEAD:refs/heads/dev", cwd=self.work)
        self.tip = git("rev-parse", "HEAD", cwd=self.work)
        Path(self.work, PATH).parent.mkdir(parents=True)
        Path(self.work, PATH).write_text("gate\n")
        git("add", "-A", cwd=self.work)
        git("commit", "-m", "candidate\n\nProject-Metadata: " + ADMISSION, cwd=self.work)
        self.head = git("rev-parse", "HEAD", cwd=self.work)

    def gate(self, *arguments):
        """Run the real entry point from the checkout under a private home, so nothing touches the user's own files."""
        env = {"HOME": self.home, "PATH": os.environ["PATH"], "PYTHONPATH": str(ROOT), "TMPDIR": self.root}
        return subprocess.run([sys.executable, "-B", "-m", "scripts.reconcile_gate", "--config", self.config, *arguments],
                              cwd=self.work, env=env, capture_output=True, text=True)

    def command(self, checkpoint="pre-push"):
        """The command line for this world's candidate head. Never `run`: that name is TestCase's own entry point."""
        return ("--checkpoint", checkpoint, "--packet", PACKET, "--candidate", self.head)

    def advance(self, path):
        """Move the remote target one commit beyond the tip, changing `path`; return the new tip."""
        git("checkout", "-q", "-b", "upstream", self.tip, cwd=self.work)
        Path(self.work, path).parent.mkdir(parents=True, exist_ok=True)
        Path(self.work, path).write_text("upstream\n")
        for args in (("add", "-A"), ("commit", "-m", "upstream"), ("push", "--quiet", self.bare, "upstream:refs/heads/dev")):
            git(*args, cwd=self.work)
        return git("rev-parse", "HEAD", cwd=self.work)

    def test_a_target_that_advanced_with_a_watched_change_blocks_the_next_run(self):
        first = self.gate(*self.command())
        self.assertEqual((first.returncode, load(first.stdout.encode())["outcome"], first.stderr.count("allowed")), (0, "current", 1))
        moved = self.advance("Makefile")
        second = self.gate(*self.command())
        record = load(second.stdout.encode())
        self.assertEqual((second.returncode, record["outcome"], record["reasons"], record["previous_sha"], record["resolved_sha"], record["upstream_paths"]),
                         (1, "material", ["WATCHED_PATH_CHANGED"], self.tip, moved, ["Makefile"]))
        third = self.gate(*self.command())  # re-running cannot walk past a refusal
        self.assertEqual((third.returncode, load(third.stdout.encode())["previous_sha"]), (1, self.tip))
        self.assertIn("blocked: material WATCHED_PATH_CHANGED", third.stderr)

    def test_an_advance_with_no_watched_change_allows_with_a_fresh_non_material_record(self):
        self.assertEqual(self.gate(*self.command()).returncode, 0)
        moved = self.advance("docs/other.md")
        done = self.gate(*self.command())
        record = load(done.stdout.encode())  # stdout is exactly the canonical record
        self.assertEqual((done.returncode, record["outcome"], record["previous_sha"], record["resolved_sha"], record["upstream_paths"]),
                         (0, "non_material", self.tip, moved, ["docs/other.md"]))
        self.assertLess((datetime.now(timezone.utc) - datetime.fromisoformat(record["observed_at"])).total_seconds(), 300)  # fresh
        recorded = seal({"checkpoint": "pre-push", "repository": "acme/widget", "ref": "refs/heads/dev", "previous_sha": self.tip, "resolved_sha": moved,
                         "candidate_sha": self.head, "packet_id": PACKET, "packet_sha256": RECEIPT["packet_sha256"], "plan_sha256": sha(PLAN_BYTES),
                         "upstream_paths": ["docs/other.md"], "outcome": "non_material", "reasons": ["NO_WATCHED_CHANGE"], "observed_at": record["observed_at"]})
        self.assertEqual(canonical_bytes(record), canonical_bytes(recorded))  # identical fixtures, the recorded decision
        self.assertEqual(self.gate(*self.command()).returncode, 0)  # an allowance never becomes the head's own baseline

    def test_a_checkpoint_outside_the_table_or_a_missing_argument_is_usage(self):
        for arguments in (("--checkpoint", "bogus", "--packet", PACKET, "--candidate", self.head),
                          ("--checkpoint", "pre-push", "--candidate", self.head), ("--checkpoint", "", "--packet", "", "--candidate", "")):
            with self.subTest(arguments=arguments):
                self.assertEqual(self.gate(*arguments).returncode, 2)

    def test_a_malformed_candidate_or_a_missing_sync_root_blocks_naming_the_cause(self):
        Path(self.private).write_text("{}")
        self.assertEqual((self.gate(*self.command()).returncode, "dropbox_local_path" in self.gate(*self.command()).stderr), (1, True))


if __name__ == "__main__":
    unittest.main()
