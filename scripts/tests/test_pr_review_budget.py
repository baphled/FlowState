"""Test protected per-type PR budget selection and untrusted input rejection."""
import unittest
from scripts.pr_review_budget import select

POLICY = '''FLOWSTATE_PR_MAX_LINES=600
FLOWSTATE_PR_MAX_LINES_FEAT=1200
FLOWSTATE_PR_MAX_LINES_FIX=800
FLOWSTATE_PR_MAX_LINES_REFACTOR=900
'''


class TypedPRBudget(unittest.TestCase):
    def test_feature_receives_larger_budget(self):
        self.assertEqual(select(POLICY, 'feat(app): streaming loop', 'feature/stream'), 1200)

    def test_fix_has_distinct_budget(self):
        self.assertEqual(select(POLICY, 'fix(store): replay text', 'fix/replay'), 800)

    def test_chore_uses_default(self):
        self.assertEqual(select(POLICY, 'chore(ci): tidy guard', 'chore/ci'), 600)

    def test_mismatch_cannot_claim_feature_budget(self):
        with self.assertRaisesRegex(ValueError, 'disagree'):
            select(POLICY, 'feat(app): larger budget', 'chore/ci')

    def test_unrecognised_type_fails_closed(self):
        with self.assertRaisesRegex(ValueError, 'Conventional'):
            select(POLICY, 'feature: bad', 'feature/ci')

    def test_invalid_budget_fails_closed(self):
        with self.assertRaisesRegex(ValueError, 'Invalid'):
            select(POLICY.replace('1200', 'unlimited'), 'feat(app): test', 'feature/test')

    def test_duplicate_policy_key_fails_closed(self):
        with self.assertRaisesRegex(ValueError, 'Duplicate'):
            select(POLICY + 'FLOWSTATE_PR_MAX_LINES=99999\n', 'fix: test', 'fix/test')

    def test_protected_push_uses_commit_type(self):
        self.assertEqual(select(POLICY, 'feat(app): squash merged'), 1200)


if __name__ == '__main__':
    unittest.main()
