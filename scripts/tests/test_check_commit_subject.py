"""Synthetic fixtures verify the local commit-subject policy checker."""
import importlib.util
import json
from pathlib import Path
import unittest

SCRIPT = Path(__file__).parents[1] / "check_commit_subject.py"
spec = importlib.util.spec_from_file_location("check_commit_subject", SCRIPT)
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class SubjectValidationTests(unittest.TestCase):
    def setUp(self):
        self.config = {
            "type-enum": [2, "always", ["feat", "fix", "docs", "chore"]],
            "scope-enum": [2, "always", ["session", "app", "store"]],
            "subject-case": [2, "always", "lower-case"],
            "subject-empty": [2, "never"],
            "subject-max-length": [2, "always", 72],
            "header-max-length": [2, "always", 100],
        }

    def test_valid_subject_passes(self):
        errors = checker.validate_header(
            "feat(session): child sessions via session new --parent", self.config)
        self.assertEqual(errors, [])

    def test_unknown_type_is_rejected(self):
        errors = checker.validate_header(
            "feature(session): wrong type word", self.config)
        self.assertEqual(len(errors), 1)
        self.assertIn("type must be one of", errors[0])

    def test_unknown_scope_is_rejected(self):
        errors = checker.validate_header(
            "feat(cli): cli is not in the scope enum", self.config)
        self.assertEqual(len(errors), 1)
        self.assertIn("scope must be one of", errors[0])

    def test_uppercase_subject_is_rejected(self):
        errors = checker.validate_header(
            "feat(session): Budget Reservations", self.config)
        self.assertEqual(len(errors), 1)
        self.assertIn("lower-case", errors[0])

    def test_oversized_subject_is_rejected(self):
        header = "feat(session): " + "x" * 73
        errors = checker.validate_header(header, self.config)
        self.assertEqual(len(errors), 1)
        self.assertIn("at most 72", errors[0])

    def test_merge_headers_are_accepted(self):
        self.assertEqual(checker.validate_header(
            "Merge branch 'next' into main for release", self.config), [])

    def test_revert_headers_are_accepted(self):
        self.assertEqual(checker.validate_header(
            'Revert "feat(session): something"', self.config), [])

    def test_config_defaults_apply_when_file_missing(self):
        errors = checker.validate_header(
            "feat: plain subject without scope", checker.DEFAULT_CONFIG)
        self.assertEqual(errors, [])


class CliTests(unittest.TestCase):
    def test_message_file_mode_reports_violations(self):
        import subprocess
        import tempfile
        message = tempfile.NamedTemporaryFile("w", delete=False, suffix=".msg")
        message.write("feat(cli): wrong scope and Type\n\nbody line\n")
        message.close()
        config = Path(__file__).parents[2] / ".commitlintrc.json"
        result = subprocess.run(
            ["python3", str(SCRIPT), "--message-file", message.name,
             "--config", str(config)],
            capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn("scope must be one of", result.stderr)


if __name__ == "__main__":
    unittest.main()
