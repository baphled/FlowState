"""Fail-closed verification of required CI checks on the exact candidate head."""
import unittest
from scripts.admission_checks import (
    AdmissionError, check_runs_page, required_check_names, verify_required_checks,
)

HEAD = "2" * 40
OTHER = "3" * 40


def check_run(name, status="completed", conclusion="success", sha=None):
    run = {"name": name, "status": status, "conclusion": conclusion}
    if sha is not None:
        run["head_sha"] = sha
    return run


class ChecksGitHub:
    """Read-only fake returning canned check-runs pages for one head."""

    def __init__(self, runs):
        self.runs = runs
        self.calls = []

    def check_runs(self, sha, page):
        self.calls.append((sha, page))
        return self.runs if page == 1 else []


class RequiredCheckNames(unittest.TestCase):
    def test_missing_or_malformed_configuration_fails_closed(self):
        for required in (None, (), [], "", ("Build", ""), (" Build"), ("Build "), (42,)):
            with self.subTest(required=required):
                with self.assertRaisesRegex(AdmissionError, "required checks"):
                    required_check_names(required)

    def test_valid_configuration_passes(self):
        self.assertEqual(required_check_names(("Build", "Test (ubuntu-latest)")),
                         ["Build", "Test (ubuntu-latest)"])


class CheckRunsPage(unittest.TestCase):
    def test_invalid_head_or_page_is_rejected(self):
        requests = RecordingRequests([])
        for sha, page in (("zzz", 1), (HEAD, 0), (HEAD, "1")):
            with self.subTest(sha=sha, page=page):
                with self.assertRaises(AdmissionError):
                    check_runs_page(requests, sha, page)
        self.assertEqual(requests.calls, [])

    def test_non_list_payload_is_rejected(self):
        with self.assertRaisesRegex(AdmissionError, "check-runs"):
            check_runs_page(RecordingRequests({"check_runs": {}}), HEAD, 1)

    def test_page_requests_the_exact_head(self):
        requests = RecordingRequests([{"name": "Build"}])
        self.assertEqual(check_runs_page(requests, HEAD, 3), [{"name": "Build"}])
        self.assertEqual(requests.calls, [("GET", f"/commits/{HEAD}/check-runs?per_page=100&page=3")])


class RecordingRequests:
    def __init__(self, runs):
        self.runs = {"check_runs": runs}
        self.calls = []

    def __call__(self, method, path):
        self.calls.append((method, path))
        return self.runs


class VerifyRequiredChecks(unittest.TestCase):
    def test_all_green_on_exact_head_passes(self):
        github = ChecksGitHub([check_run("Build", sha=HEAD),
                               check_run("Test (ubuntu-latest)", sha=HEAD)])
        verify_required_checks(HEAD, github, ("Build", "Test (ubuntu-latest)"))

    def test_skipped_required_check_fails(self):
        github = ChecksGitHub([check_run("Build", conclusion="skipped", sha=HEAD)])
        with self.assertRaisesRegex(AdmissionError, "did not succeed"):
            verify_required_checks(HEAD, github, ("Build",))

    def test_failed_required_check_fails(self):
        github = ChecksGitHub([check_run("Build", conclusion="failure", sha=HEAD)])
        with self.assertRaisesRegex(AdmissionError, "Build"):
            verify_required_checks(HEAD, github, ("Build",))

    def test_incomplete_required_check_fails(self):
        github = ChecksGitHub([check_run("Build", status="in_progress", conclusion=None)])
        with self.assertRaisesRegex(AdmissionError, "Build"):
            verify_required_checks(HEAD, github, ("Build",))

    def test_missing_required_check_fails(self):
        github = ChecksGitHub([check_run("Lint & Format", sha=HEAD)])
        with self.assertRaisesRegex(AdmissionError, "Build"):
            verify_required_checks(HEAD, github, ("Build",))

    def test_check_for_different_sha_fails(self):
        github = ChecksGitHub([check_run("Build", sha=OTHER)])
        with self.assertRaisesRegex(AdmissionError, "different head"):
            verify_required_checks(HEAD, github, ("Build",))

    def test_check_without_head_sha_binding_fails(self):
        github = ChecksGitHub([check_run("Build")])
        with self.assertRaisesRegex(AdmissionError, "different head"):
            verify_required_checks(HEAD, github, ("Build",))

    def test_missing_required_configuration_fails_closed(self):
        for required in (None, (), []):
            with self.subTest(required=required):
                with self.assertRaisesRegex(AdmissionError, "required checks"):
                    verify_required_checks(HEAD, ChecksGitHub([]), required)

    def test_checks_are_fetched_from_the_exact_head(self):
        github = ChecksGitHub([check_run("Build", sha=HEAD)])
        verify_required_checks(HEAD, github, ("Build",))
        self.assertEqual(github.calls[0], (HEAD, 1))

    def test_later_green_run_for_same_name_still_fails_on_first_verdict(self):
        github = ChecksGitHub([check_run("Build", conclusion="failure", sha=HEAD),
                               check_run("Build", sha=HEAD)])
        with self.assertRaisesRegex(AdmissionError, "failure"):
            verify_required_checks(HEAD, github, ("Build",))


if __name__ == "__main__":
    unittest.main()
