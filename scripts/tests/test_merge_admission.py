"""Offline fail-closed contract for the trusted merge admission verifier."""
import sys
import unittest
from io import BytesIO
from urllib.error import HTTPError
from pathlib import Path
from unittest.mock import patch
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from scripts.merge_admission import (  # noqa: E402
    AdmissionError, Dropbox, GitHub, execute, independent_approval,
    metadata_reference, validate_record, verify_candidate,
)
HEAD = "2" * 40
PARENT = "1" * 40
AUTHOR = "author"
REF = "dropbox:/FlowState/Metadata/attempts/example-01/commit.json"
READS = [
    "/FlowState/Dev/00-AGENT-READ-FIRST.md",
    "/FlowState/Dev/AGENTS.md",
    "/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md",
    "/FlowState/Metadata/ACTIVE.json",
]

def record(**updates):
    result = {
        "record_type": "flowstate-project-metadata-v1",
        "metadata_ref": REF,
        "base_sha": PARENT,
        "docs_pack_manifest_sha256": "a" * 64,
        "attribution": {
            "agent": "unverified", "provider": "none", "model": "none",
            "harness": "unverified", "source": "no serving identity observed",
        },
        "local_candidate": {"paths": {"scripts/demo.py": {
            "sha256": "2a97516c354b68848cdbd8f54a226a0a55b21ed138e207ad6c5cbb9c00aa5aea",
            "bytes": 4,
        }}},
        "dropbox_reads": [
            {"path": path, "file_id": f"id:{i}", "sha256": "b" * 64}
            for i, path in enumerate(READS)
        ],
    }
    result.update(updates)
    return result

def pr(**updates):
    result = {
        "state": "open", "draft": False,
        "base": {"ref": "next"}, "head": {"sha": HEAD},
        "user": {"login": AUTHOR},
    }
    result.update(updates)
    return result

def commit(message="chore: candidate\n\nProject-Metadata: " + REF, **updates):
    result = {"sha": HEAD, "parents": [{"sha": PARENT}], "commit": {"message": message},
              "author": {"login": AUTHOR}, "committer": {"login": AUTHOR}}
    result.update(updates)
    return result

def approval(**updates):
    result = {
        "user": {"login": "reviewer"}, "state": "APPROVED",
        "commit_id": HEAD, "author_association": "COLLABORATOR",
    }
    result.update(updates)
    return result

class OfflineAdmission(unittest.TestCase):
    def assertRejected(self, pattern, operation):
        with self.assertRaisesRegex(AdmissionError, pattern):
            operation()
    def test_good_exact_head_with_actual_record(self):
        self.assertEqual(
            verify_candidate(pr(), [approval()], [commit()], lambda _: record(), HEAD, FakeGitHub()), HEAD,
        )
    def test_bad_or_duplicate_trailer_fails(self):
        for message in (
            "chore: no trailer", "Project-Metadata: /tmp/a.json",
            "Project-Metadata: " + REF + "\nProject-Metadata: " + REF,
            "Project-Metadata: dropbox:/FlowState/Metadata/attempts/x/../../bad.json",
        ):
            with self.subTest(message=message):
                self.assertRejected("one valid", lambda: metadata_reference(message))
    def test_missing_external_record_fails(self):
        def missing(_):
            raise AdmissionError("Dropbox record missing")
        self.assertRejected("missing", lambda: verify_candidate(pr(), [approval()], [commit()], missing, HEAD, FakeGitHub()))
    def test_wrong_external_ref_fails(self):
        wrong = record(metadata_ref=REF.replace("commit.json", "other.json"))
        self.assertRejected("reference mismatch", lambda: validate_record(wrong, REF, PARENT))
    def test_wrong_parent_sha_fails(self):
        self.assertRejected("base SHA", lambda: validate_record(record(base_sha="f" * 40), REF, PARENT))
    def test_missing_source_read_fails(self):
        incomplete = record(dropbox_reads=record()["dropbox_reads"][:2])
        self.assertRejected("missing required", lambda: validate_record(incomplete, REF, PARENT))
    def test_bad_evidence_digest_fails(self):
        bad = record(docs_pack_manifest_sha256="invalid")
        self.assertRejected("invalid documentation", lambda: validate_record(bad, REF, PARENT))
    def test_empty_attribution_source_fails(self):
        bad = record(attribution={**record()["attribution"], "source": ""})
        self.assertRejected("attribution", lambda: validate_record(bad, REF, PARENT))
    def test_wrong_candidate_or_empty_commit_set_fails(self):
        self.assertRejected("candidate SHA", lambda: verify_candidate(pr(), [approval()], [commit()], lambda _: record(), "3" * 40, FakeGitHub()))
        self.assertRejected("incomplete", lambda: verify_candidate(pr(), [approval()], [], lambda _: record(), HEAD, FakeGitHub()))
    def test_approval_of_old_head_fails(self):
        self.assertRejected("no independent", lambda: independent_approval([approval(commit_id=PARENT)], HEAD, AUTHOR))
    def test_self_review_fails(self):
        self.assertRejected("no independent", lambda: independent_approval([approval(user={"login": AUTHOR})], HEAD, AUTHOR))
    def test_quota_comment_not_approval(self):
        review = approval(state="COMMENTED", body="Copilot quota exceeded")
        self.assertRejected("no independent", lambda: independent_approval([review], HEAD, AUTHOR))
    def test_unprivileged_reviewer_not_counted(self):
        self.assertRejected("no independent", lambda: independent_approval([approval(author_association="NONE")], HEAD, AUTHOR))
    def test_dismissal_overrides_old_approval(self):
        self.assertRejected("no independent", lambda: independent_approval([
            approval(), approval(state="DISMISSED")], HEAD, AUTHOR))
    def test_changes_requested_blocks_other_approval(self):
        self.assertRejected("changes requested", lambda: independent_approval([
            approval(), approval(user={"login": "other"}, state="CHANGES_REQUESTED")
        ], HEAD, AUTHOR))
    def test_later_approval_resolves_same_reviewer_change_request(self):
        independent_approval([approval(state="CHANGES_REQUESTED"), approval()], HEAD, AUTHOR)
    def test_draft_or_wrong_base_fails(self):
        self.assertRejected("not an open", lambda: verify_candidate(pr(draft=True), [approval()], [commit()], lambda _: record(), HEAD, FakeGitHub()))
        self.assertRejected("not an open", lambda: verify_candidate(pr(base={"ref": "main"}), [approval()], [commit()], lambda _: record(), HEAD, FakeGitHub()))
    def test_external_read_token_is_required(self):
        self.assertRejected("token", lambda: Dropbox(""))
    def test_github_token_is_required(self):
        self.assertRejected("token", lambda: GitHub("baphled/FlowState", ""))
    def test_dropbox_404_is_rejected(self):
        missing = HTTPError("https://content.dropboxapi.com", 409, "not_found", None, BytesIO())
        with patch("scripts.merge_admission.urlopen", side_effect=missing):
            self.assertRejected("record missing", lambda: Dropbox("test-token").record(REF))
    def test_tampered_same_parent_candidate_rejected(self):
        gh = FakeGitHub()
        gh.blob = lambda _: b"other"
        self.assertRejected("blob does not match", lambda: verify_candidate(
            pr(), [approval()], [commit()], lambda _: record(), HEAD, gh))
    def test_extra_or_removed_paths_rejected(self):
        gh = FakeGitHub()
        gh.commit_files = lambda _: gh.__class__.commit_files(gh, HEAD) + [
            {"filename": "extra.py", "status": "added", "sha": "a" * 40}]
        self.assertRejected("changed-path", lambda: verify_candidate(
            pr(), [approval()], [commit()], lambda _: record(), HEAD, gh))
        gh.commit_files = lambda _: [{"filename": "scripts/demo.py", "status": "removed"}]
        self.assertRejected("deletion", lambda: verify_candidate(
            pr(), [approval()], [commit()], lambda _: record(), HEAD, gh))
    def test_contributor_cannot_approve_own_code(self):
        self.assertRejected("no independent", lambda: verify_candidate(
            pr(), [approval()], [commit(author={"login": "reviewer"})],
            lambda _: record(), HEAD, FakeGitHub()))
    def test_trusted_workflow_has_no_candidate_checkout(self):
        source = (ROOT / ".github/workflows/merge-admission.yml").read_text()
        self.assertIn("ref: main", source)
        self.assertNotIn("pull_request_review:", source)
        self.assertNotIn("ref: ${{ github.event.pull_request.head", source)
        self.assertIn("statuses: write", source)
        self.assertIn("FLOWSTATE_DROPBOX_METADATA_READ_TOKEN", source)

class FakeGitHub:
    def __init__(self, change_head=False):
        self.statuses = []
        self.change_head = change_head
        self.calls = 0
    def pr(self, number):
        self.calls += 1
        return pr(head={"sha": "9" * 40}) if self.change_head and self.calls > 1 else pr()
    def pages(self, path):
        if path.endswith("/commits"):
            return [commit()]
        if path.endswith("/reviews"):
            return [approval()]
        raise AssertionError(path)
    def commit_files(self, sha):
        return [{"filename": "scripts/demo.py", "status": "modified", "sha": "c" * 40}]
    def blob(self, sha):
        return b"demo"
    def status(self, sha, state, description):
        self.statuses.append((sha, state, description))

class FakeDropbox:
    def __init__(self, missing=False):
        self.missing = missing
    def record(self, reference):
        if self.missing:
            raise AdmissionError("Dropbox record missing")
        return record()

class PublishedStatus(unittest.TestCase):
    def test_passes_only_after_actual_admission(self):
        gh = FakeGitHub()
        execute({"pull_request": {"number": 225}}, gh, FakeDropbox())
        self.assertEqual([(sha, state) for sha, state, _ in gh.statuses], [
            (HEAD, "pending"), (HEAD, "success"),
        ])
    def test_missing_record_fails_status(self):
        gh = FakeGitHub()
        with self.assertRaises(AdmissionError):
            execute({"pull_request": {"number": 225}}, gh, FakeDropbox(missing=True))
        self.assertEqual([state for _, state, _ in gh.statuses], ["pending", "failure"])
    def test_source_drift_never_gets_success(self):
        gh = FakeGitHub(change_head=True)
        with self.assertRaisesRegex(AdmissionError, "head changed"):
            execute({"pull_request": {"number": 225}}, gh, FakeDropbox())
        self.assertEqual([state for _, state, _ in gh.statuses], ["pending", "failure"])
    def test_missing_credential_after_previous_success_fails_status(self):
        gh = FakeGitHub()
        execute({"pull_request": {"number": 225}}, gh, FakeDropbox())
        with self.assertRaisesRegex(AdmissionError, "token"):
            execute({"pull_request": {"number": 225}}, gh, "")
        self.assertEqual([state for _, state, _ in gh.statuses],
                         ["pending", "success", "pending", "failure"])
    def test_bad_event_does_not_post_status(self):
        gh = FakeGitHub()
        with self.assertRaises(AdmissionError):
            execute({"pull_request": {"number": True}}, gh, FakeDropbox())
        self.assertEqual(gh.statuses, [])

if __name__ == "__main__":
    unittest.main()
