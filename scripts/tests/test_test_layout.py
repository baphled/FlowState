"""Regression tests for exact test-file ownership on committed candidates."""
import unittest

from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from test_layout import test_layout_errors  # noqa: E402


class TestTestLayout(unittest.TestCase):
    def test_exact_companion_is_allowed(self):
        paths = {"internal/storage/writer.go", "internal/storage/writer_test.go"}
        self.assertEqual(test_layout_errors(paths, ["internal/storage/writer_test.go"]), [])

    def test_acceptance_number_without_implementation_fails(self):
        paths = {"internal/storage/writer.go", "internal/storage/acceptance_items45_test.go"}
        errors = test_layout_errors(paths, ["internal/storage/acceptance_items45_test.go"])
        self.assertEqual(len(errors), 1)
        self.assertIn("acceptance_items45.go", errors[0])

    def test_fixture_and_qualification_catchalls_fail(self):
        changed = ["internal/storage/fixtures_gen_test.go",
                   "internal/storage/qualification_capture_test.go"]
        errors = test_layout_errors(set(changed) | {"internal/storage/storage.go"}, changed)
        self.assertEqual(len(errors), 2)

    def test_suite_bootstrap_is_allowed(self):
        self.assertEqual(test_layout_errors({"internal/skill/suite_test.go"},
                                            ["internal/skill/suite_test.go"]), [])

    def test_unchanged_legacy_does_not_block_unrelated_work(self):
        paths = {"internal/storage/legacy_test.go", "internal/storage/writer.go",
                 "internal/storage/writer_test.go"}
        self.assertEqual(test_layout_errors(paths, ["internal/storage/writer_test.go"]), [])

    def test_new_unmatched_test_is_rejected_even_if_renamed(self):
        paths = {"internal/runtime/loop.go", "internal/runtime/loop_cancel_test.go"}
        errors = test_layout_errors(paths, ["internal/runtime/loop_cancel_test.go"])
        self.assertEqual(len(errors), 1)

    def test_unrelated_production_path_is_not_a_test(self):
        self.assertEqual(test_layout_errors({"internal/app/usecase.go"},
                                            ["internal/app/usecase.go"]), [])


if __name__ == "__main__":
    unittest.main()
