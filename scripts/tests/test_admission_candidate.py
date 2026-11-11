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


class RequiredChecks(unittest.TestCase):
    head = "2" * 40
    other = "3" * 40

    def test_all_green_on_exact_head_passes(self):
        github = ChecksGitHub([check_run("Build", sha=self.head),
                               check_run("Test (ubuntu-latest)", sha=self.head)])
        verify_required_checks(self.head, github, ("Build", "Test (ubuntu-latest)"))

    def test_skipped_required_check_fails(self):
        github = ChecksGitHub([check_run("Build", conclusion="skipped", sha=self.head)])
        with self.assertRaisesRegex(AdmissionError, "did not succeed"):
            verify_required_checks(self.head, github, ("Build",))

    def test_failed_required_check_fails(self):
        github = ChecksGitHub([check_run("Build", conclusion="failure", sha=self.head)])
        with self.assertRaisesRegex(AdmissionError, "Build"):
            verify_required_checks(self.head, github, ("Build",))

    def test_incomplete_required_check_fails(self):
        github = ChecksGitHub([check_run("Build", status="in_progress", conclusion=None)])
        with self.assertRaisesRegex(AdmissionError, "Build"):
            verify_required_checks(self.head, github, ("Build",))

    def test_missing_required_check_fails(self):
        github = ChecksGitHub([check_run("Lint & Format", sha=self.head)])
        with self.assertRaisesRegex(AdmissionError, "Build"):
            verify_required_checks(self.head, github, ("Build",))

    def test_check_for_different_sha_fails(self):
        github = ChecksGitHub([check_run("Build", sha=self.other)])
        with self.assertRaisesRegex(AdmissionError, "different head"):
            verify_required_checks(self.head, github, ("Build",))

    def test_check_without_head_sha_binding_fails(self):
        github = ChecksGitHub([check_run("Build")])
        with self.assertRaisesRegex(AdmissionError, "different head"):
            verify_required_checks(self.head, github, ("Build",))

    def test_missing_required_configuration_fails_closed(self):
        for required in (None, (), []):
            with self.subTest(required=required):
                with self.assertRaisesRegex(AdmissionError, "required checks"):
                    verify_required_checks(self.head, ChecksGitHub([]), required)

    def test_invalid_required_entry_fails_closed(self):
        with self.assertRaisesRegex(AdmissionError, "required checks"):
            verify_required_checks(self.head, ChecksGitHub([]), ("", "Build "))

    def test_checks_are_fetched_from_the_exact_head(self):
        github = ChecksGitHub([check_run("Build", sha=self.head)])
        verify_required_checks(self.head, github, ("Build",))
        self.assertEqual(github.calls[0], (self.head, 1))
