"""Exact-base failure control for PR candidates."""
import unittest
from scripts.admission_candidate import AdmissionError, validate_candidate_state

class CandidateState(unittest.TestCase):
    def test_draft_is_rejected(self):
        with self.assertRaisesRegex(AdmissionError, "not an open"):
            validate_candidate_state({"state": "open", "draft": True,
                                      "base": {"ref": "next"}, "head": {"sha": "1"*40}},
                                     [{"sha": "1"*40}], "1"*40)
