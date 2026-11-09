"""Independent regressions for the agent-review signature verifier."""
import hashlib
import hmac
import json
import unittest

from scripts.review_attestation import (
    DOMAIN, ISSUER, ReviewAttestationError, verify_attestation,
)

KEY = "synthetic-review-attestor-secret" * 2


def signed_review(**updates):
    record = {
        "record_type": "flowstate-agent-review-v1",
        "head_sha": "2" * 40,
        "verdict": "PASS",
        "reviewer": {"run_id": "reviewer-A"},
        "negative_oracle": {"evidence": "synthetic", "result": "PASS"},
    }
    record.update(updates)
    canonical = json.dumps(record, sort_keys=True, separators=(",", ":")).encode()
    record["attestation"] = {
        "issuer": ISSUER,
        "hmac_sha256": hmac.new(KEY.encode(), DOMAIN + canonical, hashlib.sha256).hexdigest(),
    }
    return record


class ReviewSignature(unittest.TestCase):
    def test_separately_signed_review_verifies(self):
        self.assertIsNone(verify_attestation(signed_review(), KEY))

    def test_unsigned_review_fails(self):
        review = signed_review()
        review.pop("attestation")
        with self.assertRaisesRegex(ReviewAttestationError, "independently signed"):
            verify_attestation(review, KEY)

    def test_mutated_review_or_wrong_key_fails(self):
        review = signed_review()
        review["reviewer"] = {"run_id": "fabricated"}
        with self.assertRaisesRegex(ReviewAttestationError, "authentication"):
            verify_attestation(review, KEY)
        with self.assertRaisesRegex(ReviewAttestationError, "authentication"):
            verify_attestation(signed_review(), KEY[::-1])

    def test_missing_signing_authority_fails(self):
        with self.assertRaisesRegex(ReviewAttestationError, "key not configured"):
            verify_attestation(signed_review(), "")
        with self.assertRaisesRegex(ReviewAttestationError, "requires a JSON object"):
            verify_attestation([], KEY)

    def test_wrong_issuer_and_malformed_signature_fail(self):
        record = signed_review()
        record["attestation"]["issuer"] = "owner-claims-review"
        with self.assertRaisesRegex(ReviewAttestationError, "independently signed"):
            verify_attestation(record, KEY)
        record = signed_review()
        record["attestation"]["hmac_sha256"] = 1234
        with self.assertRaisesRegex(ReviewAttestationError, "signature"):
            verify_attestation(record, KEY)


if __name__ == "__main__":
    unittest.main()
