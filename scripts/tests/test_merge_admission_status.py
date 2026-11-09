"""Focused contract for the dedicated merge-admission status publisher."""
import json
import unittest
from unittest.mock import patch

from scripts.merge_admission_status import GitHubStatusPublisher, StatusPublisherError


class StatusPublisherContract(unittest.TestCase):
    def test_dedicated_credential_publishes_exact_context(self):
        publisher = GitHubStatusPublisher("baphled/FlowState", "test-app-secret")
        with patch("scripts.merge_admission_status.urlopen") as open_request:
            publisher.status("a" * 40, "success", "verified")
        request = open_request.call_args.args[0]
        self.assertEqual(request.get_header("Authorization"), "Bearer test-app-secret")
        self.assertEqual(json.loads(request.data), {
            "state": "success", "context": "flowstate/merge-admission",
            "description": "verified",
        })

    def test_missing_credentials_fail_closed(self):
        with self.assertRaisesRegex(StatusPublisherError, "credential"):
            GitHubStatusPublisher("baphled/FlowState", "")

    def test_unsafe_target_or_state_fails(self):
        publisher = GitHubStatusPublisher("baphled/FlowState", "test-app-secret")
        for sha, state in (("not-sha", "success"), ("a" * 40, "unknown")):
            with self.subTest(sha=sha, state=state):
                with self.assertRaises(StatusPublisherError):
                    publisher.status(sha, state, "unexpected")

    def test_github_status_failure_is_not_success(self):
        publisher = GitHubStatusPublisher("baphled/FlowState", "test-app-secret")
        from urllib.error import URLError
        with patch("scripts.merge_admission_status.urlopen", side_effect=URLError("denied")):
            with self.assertRaisesRegex(StatusPublisherError, "publication failed"):
                publisher.status("a" * 40, "failure", "not admitted")


if __name__ == "__main__":
    unittest.main()
