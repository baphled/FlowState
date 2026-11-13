"""Synthetic fixtures verify review handoff fail-closed contracts."""
import importlib.util
from pathlib import Path
import unittest

SCRIPT = Path(__file__).parents[1] / "review_handoff.py"
spec = importlib.util.spec_from_file_location("review_handoff", SCRIPT)
review = importlib.util.module_from_spec(spec)
spec.loader.exec_module(review)
HEAD = "a" * 40


class HandoffTests(unittest.TestCase):
    def setUp(self):
        self.pr = {"number": 260, "state": "open", "head": {"sha": HEAD}}
        self.ledger = {"F260-01": {"id": "F260-01", "status": "open",
                                     "requested_change": "Fence before execution",
                                     "test_oracle": "no calls after lease revoked"}}
        self.comment = {"body": "F260-01: fence before invoking tools",
                        "html_url": "https://github.com/baphled/FlowState/pull/260#issuecomment-1"}

    def test_open_finding_is_actionable_and_blocks(self):
        output = review.evaluate(self.pr, [self.comment], [], [], self.ledger, HEAD)
        self.assertEqual("changes_required", output["status"])
        self.assertEqual("F260-01", output["findings"][0]["id"])
        self.assertFalse(output["review_accepted"])

    def test_changed_head_fails_closed(self):
        with self.assertRaisesRegex(review.HandoffError, "changed"):
            review.evaluate(self.pr, [self.comment], [], [], self.ledger, "b" * 40)

    def test_unknown_github_finding_fails_closed(self):
        with self.assertRaisesRegex(review.HandoffError, "missing from Dropbox"):
            review.evaluate(self.pr, [{"body": "F260-99: fix race"}], [], [], self.ledger, HEAD)

    def test_unstructured_change_request_fails_closed(self):
        with self.assertRaisesRegex(review.HandoffError, "Unstructured"):
            review.evaluate(self.pr, [], [{"state": "CHANGES_REQUESTED", "body": "Fix bug"}], [], {}, HEAD)

    def test_ledger_requires_correction_evidence(self):
        with self.assertRaisesRegex(review.HandoffError, "evidence"):
            review.parse_ledger({"pr": 260, "head_sha": HEAD,
                                 "findings": [{"id": "F260-01", "status": "fixed"}]}, 260, HEAD)

    def test_stale_ledger_fails_closed(self):
        with self.assertRaisesRegex(review.HandoffError, "stale"):
            review.parse_ledger({"pr": 260, "head_sha": "b" * 40,
                                 "findings": []}, 260, HEAD)

    def test_duplicate_ids_fail_closed(self):
        item = {"id": "F260-01", "status": "open"}
        with self.assertRaisesRegex(review.HandoffError, "Duplicate"):
            review.parse_ledger({"pr": 260, "head_sha": HEAD,
                                 "findings": [item, item]}, 260, HEAD)


if __name__ == "__main__":
    unittest.main()
