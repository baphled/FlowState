"""Offline fail-closed contract for the trusted merge admission verifier."""
import sys
import unittest
from io import BytesIO
from urllib.error import HTTPError
from pathlib import Path
from unittest.mock import patch
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from scripts.admission_io import (  # noqa: E402
    AdmissionError, Dropbox, GitHub, execute, validate_agent_review,
    metadata_reference, validate_record, verify_candidate,
)
HEAD = "2" * 40
PARENT = "1" * 40
AUTHOR = "author"
REF = "dropbox:/FlowState/Metadata/attempts/example-01/commit.json"
REVIEW_REF = "dropbox:/FlowState/Metadata/attempts/example-01/agent-review.json"
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
        "number": 225, "body": "Agent-Review: " + REVIEW_REF,
        "state": "open", "draft": False,
        "base": {"ref": "next", "sha": PARENT}, "head": {"sha": HEAD},
        "user": {"login": AUTHOR},
    }
    result.update(updates)
    return result

def commit(message="chore: candidate\n\nProject-Metadata: " + REF, **updates):
    result = {"sha": HEAD, "parents": [{"sha": PARENT}], "commit": {"message": message},
              "author": {"login": AUTHOR}, "committer": {"login": AUTHOR}}
    result.update(updates)
    return result

def agent_review(**updates):
    result = {
        "record_type": "flowstate-agent-review-v1", "metadata_ref": REVIEW_REF,
        "pr_number": 225, "head_sha": HEAD, "verdict": "PASS",
        "implementer": {"run_id": "implement-1", "identity_evidence": "run log"},
        "reviewer": {"role": "verifier", "run_id": "review-2", "identity_evidence": "separate run log"},
        "negative_oracle": {"result": "PASS", "evidence": "reproduced failing fixture"},
        "findings": [], "summary": "Reviewed exact source and failure behaviour",
    }
    result.update(updates)
    return result

class OfflineAdmission(unittest.TestCase):
    def assertRejected(self, pattern, operation):
        with self.assertRaisesRegex(AdmissionError, pattern):
            operation()
    def test_good_exact_head_with_actual_record(self):
        self.assertEqual(
            verify_candidate(pr(), agent_review(), [commit()], lambda _: record(), HEAD, FakeGitHub()), HEAD,
        )
    def test_missing_external_record_fails(self):
        def missing(_):
            raise AdmissionError("Dropbox record missing")
        self.assertRejected("missing", lambda: verify_candidate(pr(), agent_review(), [commit()], missing, HEAD, FakeGitHub()))
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
        self.assertRejected("candidate SHA", lambda: verify_candidate(pr(), agent_review(), [commit()], lambda _: record(), "3" * 40, FakeGitHub()))
        self.assertRejected("incomplete", lambda: verify_candidate(pr(), agent_review(), [], lambda _: record(), HEAD, FakeGitHub()))
    def test_missing_base_or_non_linear_parent_fails(self):
        self.assertRejected("base SHA", lambda: verify_candidate(pr(base={"ref": "next"}), agent_review(),
                            [commit()], lambda _: record(), HEAD, FakeGitHub()))
        for parents in [[{"sha": "f" * 40}], [{"sha": PARENT}, {"sha": "f" * 40}]]:
            self.assertRejected("linear chain", lambda: verify_candidate(pr(), agent_review(),
                                [commit(parents=parents)], lambda _: record(), HEAD, FakeGitHub()))
    def test_old_head_and_wrong_pr_review_fail(self):
        self.assertRejected("exact candidate", lambda: validate_agent_review(agent_review(head_sha=PARENT), REVIEW_REF, HEAD, 225))
        self.assertRejected("this PR", lambda: validate_agent_review(agent_review(pr_number=226), REVIEW_REF, HEAD, 225))
    def test_same_run_and_missing_provenance_fail(self):
        self.assertRejected("separate observed", lambda: validate_agent_review(agent_review(reviewer={
            "role": "verifier", "run_id": "implement-1", "identity_evidence": "run log"}), REVIEW_REF, HEAD, 225))
        self.assertRejected("separate observed", lambda: validate_agent_review(agent_review(reviewer={
            "role": "verifier", "run_id": "review-2"}), REVIEW_REF, HEAD, 225))
    def test_quota_comment_or_rejected_review_fails(self):
        self.assertRejected("accepted verdict", lambda: validate_agent_review(agent_review(verdict="COMMENTED"), REVIEW_REF, HEAD, 225))
        self.assertRejected("accepted verdict", lambda: validate_agent_review(agent_review(verdict="REQUEST_CHANGES"), REVIEW_REF, HEAD, 225))
    def test_missing_oracle_or_fabricated_reference_fails(self):
        self.assertRejected("negative-oracle", lambda: validate_agent_review(agent_review(negative_oracle={
            "result": "SKIPPED", "evidence": "quota"}), REVIEW_REF, HEAD, 225))
        self.assertRejected("exact candidate", lambda: validate_agent_review(agent_review(metadata_ref=REF), REVIEW_REF, HEAD, 225))
    def test_missing_review_trailer_fails(self):
        self.assertRejected("agent-review record", lambda: verify_candidate(pr(body=""), agent_review(), [commit()], lambda _: record(), HEAD, FakeGitHub()))
    def test_draft_or_wrong_base_fails(self):
        self.assertRejected("not an open", lambda: verify_candidate(pr(draft=True), agent_review(), [commit()], lambda _: record(), HEAD, FakeGitHub()))
        self.assertRejected("not an open", lambda: verify_candidate(pr(base={"ref": "main"}), agent_review(), [commit()], lambda _: record(), HEAD, FakeGitHub()))
    def test_tampered_same_parent_candidate_rejected(self):
        gh = FakeGitHub()
        gh.blob = lambda _: b"other"
        self.assertRejected("blob does not match", lambda: verify_candidate(
            pr(), agent_review(), [commit()], lambda _: record(), HEAD, gh))
    def test_extra_or_removed_paths_rejected(self):
        gh = FakeGitHub()
        gh.commit_files = lambda _: gh.__class__.commit_files(gh, HEAD) + [
            {"filename": "extra.py", "status": "added", "sha": "a" * 40}]
        self.assertRejected("changed-path", lambda: verify_candidate(
            pr(), agent_review(), [commit()], lambda _: record(), HEAD, gh))
        gh.commit_files = lambda _: [{"filename": "scripts/demo.py", "status": "removed"}]
        self.assertRejected("deletion", lambda: verify_candidate(
            pr(), agent_review(), [commit()], lambda _: record(), HEAD, gh))
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
        return agent_review() if reference == REVIEW_REF else record()

class PublishedStatus(unittest.TestCase):
    def test_same_head_body_or_base_drift_is_rejected(self):
        for changes in [{"body": "altered"}, {"base": {"ref": "next", "sha": "7" * 40}},
                        {"draft": True}, {"state": "closed"}]:
            with self.subTest(changes=changes):
                gh = FakeGitHub()
                gh.pr = lambda number: pr(**changes) if gh.statuses else pr()
                with self.assertRaisesRegex(AdmissionError, "PR state changed"):
                    execute({"pull_request": {"number": 225}}, gh, FakeDropbox())

    def test_disappearing_or_changed_record_never_gets_success(self):
        for missing in [True, False]:
            with self.subTest(missing=missing):
                db = FakeDropbox()
                original = db.record
                seen = set()
                def changing(reference):
                    if reference in seen:
                        if missing:
                            raise AdmissionError("Dropbox record missing")
                        return {"changed": True}
                    seen.add(reference)
                    return original(reference)
                db.record = changing
                gh = FakeGitHub()
                with self.assertRaises(AdmissionError):
                    execute({"pull_request": {"number": 225}}, gh, db)
                self.assertEqual(gh.statuses[-1][1], "failure")

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


class AdapterContract(unittest.TestCase):
    def test_missing_tokens_are_rejected(self):
        with self.assertRaises(AdmissionError):
            Dropbox("")
        with self.assertRaises(AdmissionError):
            GitHub("baphled/FlowState", "")
    def test_bad_external_download_fails(self):
        bad = HTTPError("https://content.dropboxapi.com", 409, "not_found", None, BytesIO())
        with patch("scripts.admission_io.urlopen", side_effect=bad):
            with self.assertRaises(AdmissionError):
                Dropbox("synthetic-token").record(REF)

if __name__ == "__main__":
    unittest.main()
