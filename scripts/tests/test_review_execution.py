"""Negative controls for candidate freshness and authenticated execution binding."""
import hashlib
import unittest

from scripts.admission_metadata import AdmissionError
from scripts.review_execution import validate_execution, validate_review_scope
from scripts.review_signature import canonical_payload


class ReviewExecution(unittest.TestCase):
    def setUp(self):
        self.candidate = {"repository": "owner/repo", "pr_number": 7,
                          "head_sha": "1" * 40, "base_sha": "2" * 40}
        self.review = dict(self.candidate, issued_at=100, expires_at=200,
                           execution_ref="dropbox:/FlowState/Metadata/attempts/synthetic/execution.json",
                           reviewer={"actor_id": "r", "session_id": "rs", "run_id": "rr"},
                           implementer={"actor_id": "i", "session_id": "is", "run_id": "ir"})

    def execution(self):
        return {"record_type": "flowstate-review-execution-v1", "state": "completed",
                "verdict": "PASS", "metadata_ref": self.review["execution_ref"],
                "reviewer": dict(self.review["reviewer"]),
                "implementer": dict(self.review["implementer"]),
                "review_payload_sha256": hashlib.sha256(canonical_payload(self.review)).hexdigest()}

    def test_valid_fresh_review_and_completed_execution(self):
        validate_review_scope(self.review, self.candidate, 150)
        self.assertIsNone(validate_execution(self.review, self.execution()))

    def test_cross_repository_pr_head_and_base_replay_fail(self):
        for name in self.candidate:
            with self.subTest(field=name):
                candidate = dict(self.candidate, **{name: "different"})
                with self.assertRaises(AdmissionError):
                    validate_review_scope(self.review, candidate, 150)

    def test_expired_future_or_malformed_validity_fails(self):
        for issued, expires, now in ((100, 200, 200), (151, 200, 150),
                                     (100, 3701, 150), (100, True, 150),
                                     (100, 200, None)):
            with self.subTest(times=(issued, expires, now)):
                review = dict(self.review, issued_at=issued, expires_at=expires)
                with self.assertRaises(AdmissionError):
                    validate_review_scope(review, self.candidate, now)

    def test_explicit_merge_nonce_must_match(self):
        candidate = dict(self.candidate, admission_nonce="fresh-nonce")
        with self.assertRaisesRegex(AdmissionError, "nonce"):
            validate_review_scope(self.review, candidate, 150)

    def test_changed_or_unexecuted_receipt_fails(self):
        for field, value in (("state", "queued"), ("verdict", "QUOTA_ERROR"),
                             ("review_payload_sha256", "0" * 64),
                             ("reviewer", {"run_id": "forged"})):
            with self.subTest(field=field):
                receipt = dict(self.execution(), **{field: value})
                with self.assertRaises(AdmissionError):
                    validate_execution(self.review, receipt)

    def test_same_actor_session_or_run_fails_even_with_signed_claim(self):
        for field in ("actor_id", "session_id", "run_id"):
            with self.subTest(field=field):
                self.setUp()
                self.review["reviewer"][field] = self.review["implementer"][field]
                with self.assertRaisesRegex(AdmissionError, "not independent"):
                    validate_execution(self.review, self.execution())

    def test_missing_observed_identity_fails(self):
        self.review["reviewer"].pop("actor_id")
        with self.assertRaisesRegex(AdmissionError, "lacks actor"):
            validate_execution(self.review, self.execution())

    def test_malformed_candidate_sha_or_number_fails(self):
        for field, value in (("pr_number", True), ("pr_number", -1),
                             ("head_sha", "bad"), ("base_sha", "bad")):
            candidate = dict(self.candidate, **{field: value})
            with self.assertRaises(AdmissionError):
                validate_review_scope(self.review, candidate, 150)

    def test_boolean_review_number_cannot_alias_integer_pr(self):
        candidate = dict(self.candidate, pr_number=1)
        review = dict(self.review, pr_number=True)
        with self.assertRaisesRegex(AdmissionError, "integer"):
            validate_review_scope(review, candidate, 150)

    def test_material_unresolved_findings_fail(self):
        for severity in ("P0", "P1", "P2"):
            self.review["findings"] = [{"severity": severity, "status": "open"}]
            with self.assertRaisesRegex(AdmissionError, "unresolved material"):
                validate_execution(self.review, self.execution())
