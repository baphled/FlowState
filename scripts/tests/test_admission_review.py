"""Negative control for claimed reviewer provenance."""
import unittest
from scripts.admission_review import AdmissionError, agent_review_reference, validate_agent_review
from scripts.admission_metadata import AdmissionPending, refusal_state

class ReviewClaims(unittest.TestCase):
    def test_missing_or_forged_claim_fails(self):
        with self.assertRaises(AdmissionError):
            validate_agent_review({"record_type": "flowstate-agent-review-v1",
                                   "metadata_ref": "forged"}, "real", "1"*40, 227)

class ReviewReference(unittest.TestCase):
    REF = "dropbox:/FlowState/Metadata/attempts/a/r.json"

    def test_body_without_any_review_line_is_a_waiting_state(self):
        for body in ("", "text only", None):
            with self.subTest(body=body), self.assertRaisesRegex(
                    AdmissionPending, "^Awaiting an independent exact-head agent review$"):
                agent_review_reference(body)

    def test_duplicate_review_lines_remain_a_failure(self):
        with self.assertRaises(AdmissionError) as caught:
            agent_review_reference("Agent-Review: " + self.REF + "\nAgent-Review: " + self.REF)
        self.assertNotIsInstance(caught.exception, AdmissionPending)

    def test_malformed_review_lines_remain_a_failure(self):
        for body in ("Agent-Review: not-a-reference", "Agent-Review:", "Agent-Review:" + self.REF):
            with self.subTest(body=body), self.assertRaises(AdmissionError) as caught:
                agent_review_reference(body)
            self.assertNotIsInstance(caught.exception, AdmissionPending)

    def test_a_misspelled_review_line_is_a_failure_naming_the_spelling(self):
        for body in ("agent-review: " + self.REF, "Agent-review: " + self.REF, "AGENT-REVIEW: " + self.REF,
                     " Agent-Review: " + self.REF, "Agent Review: " + self.REF, "Agent_Review: " + self.REF,
                     "Agent-Review : " + self.REF, "x\nagent-review:"):
            with self.subTest(body=body), self.assertRaisesRegex(AdmissionError, "spelled exactly") as caught:
                agent_review_reference(body)
            self.assertNotIsInstance(caught.exception, AdmissionPending)

    def test_a_review_line_with_a_unicode_look_alike_is_a_failure_naming_the_spelling(self):
        for hyphen in ("\u2010", "\u2011", "\u2013", "\u2014", "\u2212", "\ufe63", "\uff0d"):
            for body in ("Agent" + hyphen + "Review: " + self.REF, "x\nAGENT" + hyphen + "review: " + self.REF):
                with self.subTest(body=body), self.assertRaisesRegex(AdmissionError, "spelled exactly") as caught:
                    agent_review_reference(body)
                self.assertNotIsInstance(caught.exception, AdmissionPending)

    def test_a_review_line_with_invisible_or_compatibility_characters_is_a_failure(self):
        for body in ("Agent\u200b-Review: " + self.REF, "Agent-\u00adReview: " + self.REF,
                     "\uff21gent-Review: " + self.REF):
            with self.subTest(body=body), self.assertRaisesRegex(AdmissionError, "spelled exactly"):
                agent_review_reference(body)

    def test_a_valid_line_beside_a_unicode_look_alike_is_a_failure(self):
        with self.assertRaisesRegex(AdmissionError, "spelled exactly"):
            agent_review_reference("Agent-Review: " + self.REF + "\nAgent\u2011Review: " + self.REF)

    def test_a_valid_line_beside_a_misspelled_one_is_a_failure(self):
        with self.assertRaisesRegex(AdmissionError, "spelled exactly"):
            agent_review_reference("Agent-Review: " + self.REF + "\nagent-review: " + self.REF)

    def test_text_merely_mentioning_the_review_is_still_waiting(self):
        with self.assertRaises(AdmissionPending):
            agent_review_reference("See the Agent-Review: line below\nnot an agent review yet")

    def test_single_valid_line_is_returned(self):
        self.assertEqual(agent_review_reference("x\nAgent-Review: " + self.REF), self.REF)


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


class WaitingState(unittest.TestCase):
    def test_pending_is_an_admission_error(self):
        self.assertTrue(issubclass(AdmissionPending, AdmissionError))

    def test_only_a_pending_refusal_is_published_as_pending(self):
        self.assertEqual([refusal_state(AdmissionPending('w')), refusal_state(AdmissionError('f')),
                          refusal_state(ValueError('x'))], ['pending', 'failure', 'failure'])
