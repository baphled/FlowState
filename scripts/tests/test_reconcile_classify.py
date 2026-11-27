import random
import unittest
from unittest import mock
from scripts.reconcile_classify import MAX_PATHS, classify
from scripts.reconcile_paths import WorkExceeded

A, B = "a" * 40, "b" * 40
WATCHED = ["scripts/reconcile_*.py", "Makefile", ".githooks/**", "**/go.mod"]


class OutcomeTest(unittest.TestCase):
    def test_equal_shas_are_current_whatever_the_paths(self):
        for paths, watched in ((None, WATCHED), ([], WATCHED), (["Makefile"], WATCHED), (["bad path"], None)):
            self.assertEqual(classify(A, A, paths, watched), ("current", ["SHA_EQUAL"]))

    def test_changes_only_outside_watched_patterns_are_non_material(self):
        self.assertEqual(classify(A, B, ["docs/a.md", "src/x/y.go"], WATCHED), ("non_material", ["NO_WATCHED_CHANGE"]))

    def test_empty_change_list_or_no_watched_patterns_is_non_material(self):
        self.assertEqual(classify(A, B, [], WATCHED)[0], "non_material")
        self.assertEqual(classify(A, B, ["Makefile"], [])[0], "non_material")

    def test_changed_watched_path_is_material(self):
        for path in ("Makefile", "scripts/reconcile_gate.py", ".githooks/pre-push", "a/b/go.mod", "go.mod"):
            self.assertEqual(classify(A, B, ["docs/a.md", path], WATCHED), ("material", ["WATCHED_PATH_CHANGED"]))

    def test_ungrammatical_path_is_material(self):
        for path in ("", "a//b", "/abs", "a/../b", ".git/config", "a b", "a\\b", "caf\u00e9", "a/*", "x" * 513, "a/"):
            self.assertEqual(classify(A, B, ["docs/a.md", path], WATCHED), ("material", ["UNGRAMMATICAL_PATH"]), path)

    def test_both_material_reasons_are_reported_sorted(self):
        self.assertEqual(classify(A, B, ["Makefile", "a b"], WATCHED),
                         ("material", ["UNGRAMMATICAL_PATH", "WATCHED_PATH_CHANGED"]))


class IndeterminateTest(unittest.TestCase):
    def test_absent_or_malformed_path_list_is_indeterminate(self):
        for paths in (None, "Makefile", ("Makefile",), {"Makefile": 1}, ["Makefile", None], [1], [["a"]], [b"a"]):
            self.assertEqual(classify(A, B, paths, WATCHED), ("indeterminate", ["UPSTREAM_PATHS_UNKNOWN"]), paths)

    def test_more_than_the_path_limit_is_indeterminate_and_the_limit_itself_is_not(self):
        self.assertEqual(MAX_PATHS, 200)
        self.assertEqual(classify(A, B, ["Makefile"] * 201, WATCHED), ("indeterminate", ["TOO_MANY_PATHS"]))
        self.assertEqual(classify(A, B, ["Makefile"] * 200, WATCHED)[0], "material")

    def test_absent_or_malformed_shas_are_indeterminate(self):
        for previous, fresh in ((None, B), (A, None), ("", B), (A, "B" * 40), (A, "b" * 39), (1, 2), (None, None), ("x", "x")):
            self.assertEqual(classify(previous, fresh, ["docs/a.md"], WATCHED), ("indeterminate", ["SHA_MALFORMED"]))

    def test_absent_or_malformed_watched_patterns_are_indeterminate(self):
        for watched in (None, "Makefile", ("Makefile",), [None], ["a b"], ["/abs"], ["a/***"], ["x"] * 33):
            self.assertEqual(classify(A, B, ["docs/a.md"], watched), ("indeterminate", ["WATCHED_MALFORMED"]), watched)

    def test_pathological_patterns_exhaust_one_shared_budget_with_the_real_matcher(self):
        path = "/".join(["a"] * 255) + "/b"
        pattern = "/".join(["**", "a"] * 84) + "/c"
        self.assertEqual(classify(A, B, [path] * 200, [pattern] * 32), ("indeterminate", ["MATCH_WORK_EXCEEDED"]))

    def test_exhausted_matcher_work_budget_is_indeterminate(self):
        with mock.patch("scripts.reconcile_classify.match", side_effect=WorkExceeded("budget")):
            self.assertEqual(classify(A, B, ["docs/a.md"], WATCHED), ("indeterminate", ["MATCH_WORK_EXCEEDED"]))


class PropertyTest(unittest.TestCase):
    def _segment(self, rng):
        return rng.choice("abcdefgh") + "".join(rng.choice("abcdefgh0123_-") for _ in range(rng.randint(0, 12)))

    def _outside(self, rng):
        """A grammatical path built to avoid every pattern in WATCHED: a `docs`/`src` root and a `.txt` leaf."""
        middle = [self._segment(rng) for _ in range(rng.randint(0, 4))]
        return "/".join([rng.choice(("docs", "src"))] + middle + [self._segment(rng) + ".txt"])

    def test_random_paths_outside_every_watched_pattern_never_yield_material(self):
        rng = random.Random(20261126)
        for _ in range(300):
            paths = [self._outside(rng) for _ in range(rng.randint(0, 20))]
            outcome, reasons = classify(A, B, paths, WATCHED)
            self.assertEqual((outcome, reasons), ("non_material", ["NO_WATCHED_CHANGE"]), paths)


if __name__ == "__main__":
    unittest.main()
