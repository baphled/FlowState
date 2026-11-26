import time
import unittest
from scripts.reconcile_glob import match, match_ci, match_external
from scripts.reconcile_paths import Work, WorkExceeded

T, F = True, False
# Reference vectors (D0 r4 matcher-vectors.json), kind "match": (pattern, path, expected).
MATCH = (
    ("scripts/*.py", "scripts/a.py", T), ("scripts/*.py", "scripts/sub/a.py", F), ("scripts/**", "scripts/a.py", T),
    ("scripts/**", "scripts/sub/deep/a.py", T), ("scripts/**", "scripts", T), ("scripts/**", "scriptsx/a", F),
    ("**/*.go", "a.go", T), ("**/*.go", "x/y/a.go", T), ("**/*.go", "a.txt", F), ("a/**/b", "a/b", T),
    ("a/**/b", "a/x/y/b", T), ("a/**/b", "a/x/c", F), ("*", "a", T), ("*", "a/b", F), ("Makefile", "Makefile", T),
    ("Makefile", "makefile", F), (".github/**", ".github/workflows/x.yml", T), (".github/**", ".github", T),
    ("scripts/admission_*.py", "scripts/admission_a.py", T), ("scripts/admission_*.py", "scripts/admission_.py", T),
    ("scripts/admission_*.py", "scripts/admission/a.py", F), ("*", ".hidden", T), ("scripts/*.py", "scripts/a b.py", F),
    ("**", "../etc/passwd", F), ("**/*", "a/../etc", F), ("**", "/abs", F), ("**", "a//b", F), ("**", "a/", F),
    ("**", "a\\b", F), ("**", "a.py\n", F), ("**", "", F), ("**", "caf\u00e9.py", F), ("**/*", ".git/config", F),
    ("**/*", "x/.git/config", F), ("a/../b", "a/../b", F), ("a/b/", "a/b", F), ("a**/b", "ab/b", F),
    ("a/***", "a/x", F), ("a?.py", "ab.py", F), ("a[bc].py", "ab.py", F), ("scripts/reconcile_*.py", "scripts/reconcile_plan.py", T),
    ("scripts/reconcile_*.py", "scripts/tests/reconcile_plan.py", F), ("go.mod", "go.mod", T),
    ("*a*a*a*a*b", "aaaaaaaaaaaa", F), ("**/a/**/b", "a/b", T), ("**/b", "a/b", T), ("a/**/**/b", "a/b", T),
)
HEAVY = "a*" * 49 + "b"


class MatchTest(unittest.TestCase):
    def test_reference_match_vectors(self):
        for pattern, path, expected in MATCH:
            with self.subTest(pattern=pattern, path=path):
                self.assertIs(match(pattern, path), expected)

    def test_non_string_input_never_matches(self):
        self.assertEqual([match(None, "a"), match("a", None), match(1, 1)], [F, F, F])

    def test_match_ci_folds_case_for_protected_globs(self):
        pairs = (("Makefile", "makefile", T), ("scripts/*.py", "SCRIPTS/A.PY", T), (".github/**", ".GITHUB/x", T),
                 ("a/b", "a/c", F), ("**", "a/../b", F), (None, "a", F), ("a", None, F))
        for pattern, path, expected in pairs:
            with self.subTest(pattern=pattern, path=path):
                self.assertIs(match_ci(pattern, path), expected)

    def test_match_external_strips_the_prefix_from_both_sides(self):
        root = "/FlowState/Metadata/attempts/a/**"
        pairs = ((root, "/FlowState/Metadata/attempts/a/x.json", T), (root, "/FlowState/Metadata/attempts/b/x.json", F),
                 (root, "Metadata/attempts/a/x.json", F), ("Metadata/**", "/FlowState/Metadata/x", F), (None, None, F))
        for pattern, path, expected in pairs:
            with self.subTest(pattern=pattern, path=path):
                self.assertIs(match_external(pattern, path), expected)


class MatchBudgetTest(unittest.TestCase):
    def test_match_raises_when_the_supplied_budget_is_too_small(self):
        self.assertRaises(WorkExceeded, match, "a*b", "aaaa", Work(1))

    def test_match_spends_the_reference_cost_in_cells(self):
        # table n*m+1, plus pattern*text per wildcard segment or 1 per literal one, each distinct pair once (memoised).
        for pattern, path, cost in (("a*b/a*b", "aab/aab", 14), ("a/b", "a/b", 7), ("ab/cd", "ab/cd", 7), ("a*/**/c", "ab/x/c", 17)):
            with self.subTest(pattern=pattern):
                work = Work()
                match(pattern, path, work)
                self.assertEqual(500000 - work.left, cost)

    def test_a_realistic_scope_check_stays_within_budget(self):
        work = Work()
        allowed = ("scripts/reconcile_plan.py", "scripts/tests/test_reconcile_plan.py", "docs/guides/**", "scripts/*.py")
        paths = ("scripts/reconcile_plan.py", "docs/guides/reconciliation.md")
        self.assertTrue(all(any(match(a, p, work) for a in allowed) for p in paths))

    def test_maximal_adversarial_scope_check_is_cut_off_under_one_second(self):
        segment = lambda i, j: "a" * 97 + "%03d" % ((i * 7 + j) % 1000)
        paths = ["a/" + "/".join(segment(i, j) for j in range(4)) + "/f%d" % i for i in range(200)]
        allowed = ["a/" + "/".join(["a*" * 47 + "b%02d" % k] * 4) + "/**" for k in range(32)]
        work, started = Work(), time.perf_counter()
        with self.assertRaises(WorkExceeded):
            [any(match(a, p, work) for a in allowed) for p in paths]
        self.assertLess(time.perf_counter() - started, 1.0)


class TimingTest(unittest.TestCase):
    def timed(self, call):
        started = time.perf_counter()
        result = call()
        self.assertLess(time.perf_counter() - started, 1.0)
        return result

    def test_pathological_segment_glob_finishes_under_one_second(self):
        self.assertIs(self.timed(lambda: match("*a" * 12 + "b", "a" * 40)), F)

    def test_five_maximal_heavy_segments_finish_under_one_second(self):
        self.assertIs(self.timed(lambda: match("/".join([HEAVY] * 5), "/".join(["a" * 100] * 5))), F)


if __name__ == "__main__":
    unittest.main()
