import unittest
from unittest import mock
from scripts import reconcile_protect
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_protect import MINIMUM, effective_globs, protects
from scripts.reconcile_paths import Work, WorkExceeded, is_pattern

LISTED = (".github/**", ".githooks/**", "rules/**", "Makefile", "go.mod", ".env.ci", "reconcile.json",
          "scripts/base_checkers.py", "scripts/reconcile_*.py", "scripts/admission_*.py", "scripts/review_*.py",
          "scripts/status_issuer.py", "scripts/requirements-admission.txt", "scripts/tests/test_reconcile_*.py",
          "scripts/tests/test_admission_*.py", "scripts/tests/test_base_checkers.py")
PROTECTED = (".github", ".github/workflows/ci.yml", ".GITHOOKS/pre-push", "Rules/x.md", "makefile", "go.mod", "scripts/base_checkers.py",
             "scripts/reconcile_x.py", "scripts/admission_local.py", "scripts/review_x.py", "scripts/status_issuer.py", "scripts/requirements-admission.txt",
             "scripts/tests/test_reconcile_x.py", "scripts/tests/test_admission_x.py", "scripts/tests/test_base_checkers.py")
FREE = ("docs/x.md", "scripts/x.py", "scripts/sub/reconcile_x.py", "scripts/tests/test_x.py", "Makefile.bak", "go.module", "scripts/status_issuer.py.bak", "RULES.md")


class EffectiveGlobsTest(unittest.TestCase):
    def test_the_built_in_minimum_is_the_listed_set_and_is_grammatical(self):
        self.assertEqual(MINIMUM, LISTED)
        self.assertTrue(all(map(is_pattern, MINIMUM)))

    def test_additions_follow_the_minimum_without_duplicates(self):
        for configured, tail in ((MINIMUM, ()), (MINIMUM + ("docs/**", "Makefile", "docs/**"), ("docs/**",))):
            with self.subTest(configured=configured):
                self.assertEqual(effective_globs(configured), MINIMUM + tail)

    def test_a_configuration_that_drops_a_minimum_entry_is_refused_naming_it(self):
        for entry in MINIMUM:
            with self.subTest(entry=entry), self.assertRaises(ReconcileError) as caught:
                effective_globs([item for item in MINIMUM if item != entry])
            self.assertIn(entry, str(caught.exception))
        with self.assertRaisesRegex(ReconcileError, "minimum"):
            effective_globs([])

    def test_an_ungrammatical_or_non_list_configuration_is_refused(self):
        for configured in ("rules/**", None, 7, ["rules/**", "../x"], ["a b"], ["/abs"], [5], [None]):
            with self.subTest(configured=configured), self.assertRaises(ReconcileError):
                effective_globs(configured)
        with self.assertRaisesRegex(ReconcileError, "grammar"):  # the whole minimum is present: only the addition is bad
            effective_globs(MINIMUM + ("a b", 5))

    def test_an_empty_effective_set_refuses_every_transition(self):
        with mock.patch.object(reconcile_protect, "MINIMUM", ()), self.assertRaisesRegex(ReconcileError, "empty"):
            effective_globs([])


class ProtectsTest(unittest.TestCase):
    def test_minimum_and_additions_protect_case_folded_and_wildcards_stay_inside_a_segment(self):
        self.assertEqual([protects(MINIMUM, path) for path in PROTECTED], [True] * len(PROTECTED))
        self.assertEqual([protects(MINIMUM, path) for path in FREE], [False] * len(FREE))
        self.assertEqual([protects(effective_globs(MINIMUM + ("docs/**",)), path) for path in ("docs/guide.md", "docs/guide", "../Makefile")],
                         [True, True, False])

    def test_a_spent_work_budget_raises_instead_of_answering(self):
        self.assertRaises(WorkExceeded, protects, MINIMUM, "Makefile", Work(1))


if __name__ == "__main__":
    unittest.main()
