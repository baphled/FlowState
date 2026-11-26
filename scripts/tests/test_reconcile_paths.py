import time
import unittest
from scripts.reconcile_paths import (Work, WorkExceeded, has_wildcard, is_allowed_pattern, is_external_literal,
                                     is_external_pattern, is_literal_path, is_pattern, overlaps)

T, F = True, False
LONG = "b" * 100
DEEP = "/".join(["a"] * 256)

# Reference vectors (D0 r4 matcher-vectors.json), grammar kinds: (value, expected).
LITERAL = (
    ("scripts/a.py", True), ("a/../b", False), ("a/*", False), ("a/b/", False), ("/abs", False),
    (".git/x", False), ("a/./b", False), (".gitignore", True), (".github/x", True), (".git", False),
    (".GIT", False), (".Git", False), (".git.", False), ("a/.git/b", False), ("...", False),
    ("a/" + LONG, True), ("a/" + LONG + "b", False), ("makefile", True), (".env.ci", True),
    ("SCRIPTS/x.py", True), ("", False), ("a//b", False), ("a\\b", False), ("a b", False),
    ("a.py\n", False), ("caf\u00e9.py", False), ("e\u0301.py", False), ("a\x00b", False), (None, False),
    (DEEP, True), (DEEP + "/a", False),
)
ALLOWED = (
    ("scripts/*.py", True), ("docs/**", True), ("**", False), ("**/x.py", False), ("Makefile", True),
    ("a/../b", False), ("/abs", False), ("a//b", False), ("a/", False), ("a**", False), ("", False),
    ("*/**", False), ("*", False), ("*/*/**", False), ("s*/x", False), ("*.py", False), ("**/x", False),
    ("scripts/*", True), (".Git/x", False), ("a/.GIT/b", False), ("a/.git./x", False), (".git../x", False),
    ("a/.../b", False), ("a/...x", True), (".github/**", True), ("a/" + LONG, True),
    ("a/" + LONG + "b", False), (DEEP + "/a", False), (DEEP, True), ("a/***", False), ("a?.py", False),
    ("a[bc].py", False), (None, False),
)
WATCHED = (
    ("**", True), ("**/x.py", True), ("scripts/admission_*.py", True), ("a/", False), ("../x", False),
    ("*", True), ("**/x", True), (".Git/**", False), ("a**/b", False), ("**/*", True), ("a/***", False),
)
EXTERNAL = (
    ("/FlowState/Knowledge/x.md", True), ("/FlowState/a/../b", False), ("/Other/x", False),
    ("FlowState/x", False), ("/FlowState/", False), (None, False),
)

OVERLAP = (
    ("scripts/*.py", "scripts/reconcile_*.py", T), ("scripts/**", "scripts/reconcile_*.py", T),
    ("docs/**", "scripts/reconcile_*.py", F), ("scripts/tests/test_reconcile_*.py", "scripts/reconcile_*.py", F),
    ("a*", "*b", T), ("a*", "b*", F), ("**", "x/y", T), ("x/**", "y/**", F), ("x/**/z", "x/q/**", T),
    ("scripts/a*c", "scripts/*b*", T), ("a/../b", "a", T), ("a", "b/", T),
    ("ab", "ac", F), ("ab", "ab", T), ("a*c", "ab", F), ("a/ab", "a/ac", F), ("x*y", "xz*", T), ("x*y", "x*z*y", T),
)


class GrammarTest(unittest.TestCase):
    def check(self, predicate, table):
        for value, expected in table:
            with self.subTest(value=value):
                self.assertIs(predicate(value), expected)

    def test_literal_path_vectors(self):
        self.check(is_literal_path, LITERAL)

    def test_allowed_pattern_requires_a_literal_first_segment(self):
        self.check(is_allowed_pattern, ALLOWED)

    def test_watched_pattern_may_start_with_a_wildcard(self):
        self.check(is_pattern, WATCHED)

    def test_external_literal_vectors(self):
        self.check(is_external_literal, EXTERNAL)

    def test_external_pattern_needs_prefix_and_literal_first_segment(self):
        self.check(is_external_pattern, (("/FlowState/Metadata/**", True), ("/FlowState/**", False),
                                         ("/Other/x/**", False), ("/FlowState/", False), (None, False)))

    def test_has_wildcard(self):
        self.check(has_wildcard, (("a/*", True), ("a/**", True), ("a/b", False)))

    def test_literal_path_is_never_a_pattern_with_wildcards(self):
        self.assertEqual([is_pattern("a/*"), is_literal_path("a/*")], [True, False])


class OverlapTest(unittest.TestCase):
    def test_reference_overlap_vectors(self):
        for pattern, other, expected in OVERLAP:
            with self.subTest(pattern=pattern, other=other):
                self.assertIs(overlaps(pattern, other), expected)

    def test_overlap_is_symmetric_on_the_reference_vectors(self):
        for pattern, other, expected in OVERLAP:
            with self.subTest(pattern=pattern, other=other):
                self.assertIs(overlaps(other, pattern), expected)

    def test_overlap_of_120_segment_patterns_finishes_under_one_second(self):
        started = time.perf_counter()
        result = overlaps("/".join(["*a*"] * 120), "/".join(["*b*"] * 120))
        self.assertEqual((result, time.perf_counter() - started < 1.0), (T, T))


class WorkMeterTest(unittest.TestCase):
    def test_spend_raises_once_the_budget_is_exhausted(self):
        work = Work(10)
        work.spend(10)
        self.assertRaises(WorkExceeded, work.spend, 1)

    def test_overlap_raises_when_the_supplied_budget_is_too_small(self):
        self.assertRaises(WorkExceeded, overlaps, "a*b", "c*d", Work(1))

    def test_overlap_spends_the_reference_cost_in_cells(self):
        work = Work()
        overlaps("a*/b", "a*/b", work)
        self.assertEqual(work.left, 500000 - 18)

    def test_overlap_of_maximal_heavy_patterns_spends_the_reference_cost(self):
        work = Work()
        overlaps("/".join(["a*" * 49 + "b"] * 5), "/".join(["*a" * 49 + "c*"] * 5), work)
        self.assertEqual(work.left, 500000 - 50526)


if __name__ == "__main__":
    unittest.main()
