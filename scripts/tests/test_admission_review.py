"""Negative control for claimed reviewer provenance."""
import unittest
from scripts.admission_review import AdmissionError, validate_agent_review

class ReviewClaims(unittest.TestCase):
    def test_missing_or_forged_claim_fails(self):
        with self.assertRaises(AdmissionError):
            validate_agent_review({"record_type": "flowstate-agent-review-v1",
                                   "metadata_ref": "forged"}, "real", "1"*40, 227)

class AuthenticatedReview(unittest.TestCase):
    def setUp(self):
        import hashlib
        from cryptography.hazmat.primitives import serialization
        from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
        from scripts.review_signature import canonical_payload
        self.reference = "dropbox:/FlowState/Metadata/attempts/synthetic/review.json"
        self.candidate = {"repository": "owner/repo", "pr_number": 7,
                          "head_sha": "1" * 40, "base_sha": "2" * 40}
        self.review = dict(self.candidate, record_type="flowstate-agent-review-v1",
                           metadata_ref=self.reference, issued_at=100, expires_at=200,
                           reviewer={"role": "verifier", "actor_id": "r", "session_id": "rs",
                                     "run_id": "rr", "identity_evidence": "trusted harness"},
                           implementer={"actor_id": "i", "session_id": "is", "run_id": "ir",
                                        "identity_evidence": "trusted harness"},
                           negative_oracle={"result": "PASS", "evidence": "synthetic"},
                           findings=[], summary="synthetic test review", verdict="PASS",
                           execution_ref="dropbox:/FlowState/Metadata/attempts/synthetic/execution.json")
        private = Ed25519PrivateKey.from_private_bytes(bytes(range(32)))
        self.public = private.public_key().public_bytes(
            serialization.Encoding.Raw, serialization.PublicFormat.Raw).hex()
        payload = canonical_payload(self.review)
        self.review["attestation"] = {"algorithm": "Ed25519",
            "key_id": hashlib.sha256(bytes.fromhex(self.public)).hexdigest(),
            "signature": private.sign(payload).hex()}
        self.execution = {"record_type": "flowstate-review-execution-v1", "state": "completed",
            "verdict": "PASS", "metadata_ref": self.review["execution_ref"],
            "review_payload_sha256": hashlib.sha256(payload).hexdigest(),
            "reviewer": self.review["reviewer"], "implementer": self.review["implementer"]}

    def authenticate(self, authority):
        from scripts.admission_review import authenticate_agent_review
        return authenticate_agent_review(self.review, self.reference, self.candidate,
                                         self.public, authority, 150)

    def test_signature_and_authenticated_execution_are_both_required(self):
        self.assertIsNone(self.authenticate(lambda reference: self.execution))

    def test_unsigned_fabricated_review_never_authorises_admission(self):
        self.review.pop("attestation")
        with self.assertRaises(AdmissionError):
            self.authenticate(lambda reference: self.execution)

    def test_valid_signature_cannot_replace_actual_execution(self):
        with self.assertRaisesRegex(AdmissionError, "execution authority unavailable"):
            self.authenticate(None)
        with self.assertRaisesRegex(AdmissionError, "does not match"):
            self.authenticate(lambda reference: None)

    def test_authority_failure_propagates_without_acceptance(self):
        def unavailable(reference):
            raise AdmissionError("execution authority unavailable")
        with self.assertRaisesRegex(AdmissionError, "authority unavailable"):
            self.authenticate(unavailable)
