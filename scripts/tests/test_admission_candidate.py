"""Exact-base failure control for PR candidates."""
import unittest
from scripts.admission_candidate import (
    AdmissionError, validate_candidate_state, candidate_snapshot, verify_required_checks,
)

class CandidateSnapshot(unittest.TestCase):
    def test_same_head_description_change_changes_snapshot(self):
        initial = {"head": {"sha": "1" * 40}, "base": {"ref": "next", "sha": "2" * 40},
                   "state": "open", "draft": False, "body": "original"}
        self.assertNotEqual(candidate_snapshot(initial), candidate_snapshot(dict(initial, body="altered")))

    def test_every_admission_sensitive_field_changes_snapshot(self):
        initial = {"number": 225, "head": {"sha": "1" * 40},
                   "base": {"ref": "next", "sha": "2" * 40},
                   "state": "open", "draft": False, "body": "original",
                   "user": {"login": "author"}}
        for field, value in [("head", {"sha": "3" * 40}), ("base", {"ref": "next", "sha": "4" * 40}),
                             ("base", {"ref": "main", "sha": "2" * 40}), ("state", "closed"),
                             ("draft", True), ("number", 226), ("user", {"login": "other"})]:
            with self.subTest(field=field, value=value):
                self.assertNotEqual(candidate_snapshot(initial), candidate_snapshot(dict(initial, **{field:value})))

class CandidateState(unittest.TestCase):
    def test_draft_is_rejected(self):
        with self.assertRaisesRegex(AdmissionError, "not an open"):
            validate_candidate_state({"state": "open", "draft": True,
                                      "base": {"ref": "next"}, "head": {"sha": "1"*40}},
                                     [{"sha": "1"*40}], "1"*40)



