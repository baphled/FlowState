"""Negative control for claimed reviewer provenance."""
import unittest
from scripts.admission_review import AdmissionError, validate_agent_review

class ReviewClaims(unittest.TestCase):
    def test_missing_or_forged_claim_fails(self):
        with self.assertRaises(AdmissionError):
            validate_agent_review({"record_type": "flowstate-agent-review-v1",
                                   "metadata_ref": "forged"}, "real", "1"*40, 227)
