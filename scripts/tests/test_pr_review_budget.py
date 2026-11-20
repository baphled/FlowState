"""Test protected per-type PR budget selection and untrusted input rejection."""
import contextlib
import io
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from scripts.pr_review_budget import main, select, select_files

POLICY = '''FLOWSTATE_PR_MAX_LINES=600
FLOWSTATE_PR_MAX_LINES_FEAT=1200
FLOWSTATE_PR_MAX_LINES_FIX=800
FLOWSTATE_PR_MAX_LINES_REFACTOR=900
'''

FILES_POLICY = '''FLOWSTATE_PR_MAX_LINES=600
FLOWSTATE_PR_MAX_LINES_FEAT=1200
FLOWSTATE_PR_MAX_FILES=10
FLOWSTATE_PR_MAX_FILES_FEAT=15
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


class TypedPRFilesBudget(unittest.TestCase):
    def test_feature_receives_larger_files_budget(self):
        self.assertEqual(select_files(FILES_POLICY, 'feat(app): serve skeleton', 'feature/serve'), 15)

    def test_fix_uses_default_files_budget(self):
        self.assertEqual(select_files(FILES_POLICY, 'fix(store): replay text', 'fix/replay'), 10)

    def test_mismatch_cannot_claim_feature_files_budget(self):
        with self.assertRaisesRegex(ValueError, 'disagree'):
            select_files(FILES_POLICY, 'feat(app): wider paths', 'chore/ci')

    def test_malformed_policy_fails_closed(self):
        with self.assertRaisesRegex(ValueError, 'Malformed'):
            select_files('FLOWSTATE_PR_MAX_FILES\n', 'feat(app): wider paths', 'feature/wide')

    def test_invalid_files_budget_fails_closed(self):
        with self.assertRaisesRegex(ValueError, 'Invalid'):
            select_files(FILES_POLICY.replace('=15', '=unlimited'),
                         'feat(app): wider paths', 'feature/wide')

    def test_missing_files_budget_fails_closed(self):
        with self.assertRaisesRegex(ValueError, 'Invalid'):
            select_files(POLICY, 'feat(app): wider paths', 'feature/wide')

    def test_lines_and_files_budgets_are_independent(self):
        self.assertEqual(select(FILES_POLICY, 'feat(app): serve skeleton', 'feature/serve'), 1200)
        self.assertEqual(select_files(FILES_POLICY, 'feat(app): serve skeleton', 'feature/serve'), 15)


class MainExitCodes(unittest.TestCase):
    """main() separates classification failures from policy/IO failures."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.policy = Path(self._tmp.name) / 'env.ci'

    def tearDown(self):
        self._tmp.cleanup()

    def invoke(self, *args):
        argv = ['pr_review_budget.py', '--policy', str(self.policy), *args]
        stdout, stderr = io.StringIO(), io.StringIO()
        with mock.patch.object(sys, 'argv', argv), \
                contextlib.redirect_stdout(stdout), \
                contextlib.redirect_stderr(stderr):
            code = main()
        return code, stderr.getvalue()

    def test_unclassified_subject_exits_two(self):
        self.policy.write_text(POLICY, encoding='utf-8')
        code, stderr = self.invoke('--title', 'v1.2.3 release aggregate')
        self.assertEqual(code, 2)
        self.assertIn('PR review budget blocked: ', stderr)

    def test_malformed_policy_exits_one(self):
        self.policy.write_text('FLOWSTATE_PR_MAX_LINES\n', encoding='utf-8')
        code, _ = self.invoke('--title', 'feat(app): streaming loop')
        self.assertEqual(code, 1)

    def test_missing_policy_file_exits_one(self):
        code, _ = self.invoke('--title', 'feat(app): streaming loop')
        self.assertEqual(code, 1)

    def test_valid_selection_exits_zero(self):
        self.policy.write_text(POLICY, encoding='utf-8')
        code, stderr = self.invoke('--title', 'fix(store): replay text',
                                   '--branch', 'fix/replay')
        self.assertEqual(code, 0)
        self.assertEqual(stderr, '')

    def test_files_flag_selects_files_budget(self):
        self.policy.write_text(FILES_POLICY, encoding='utf-8')
        stdout, stderr = io.StringIO(), io.StringIO()
        argv = ['pr_review_budget.py', '--policy', str(self.policy), '--title',
                'feat(app): serve skeleton', '--branch', 'feature/serve', '--files']
        with mock.patch.object(sys, 'argv', argv), \
                contextlib.redirect_stdout(stdout), \
                contextlib.redirect_stderr(stderr):
            code = main()
        self.assertEqual(code, 0)
        self.assertEqual(stdout.getvalue().strip(), '15')


if __name__ == '__main__':
    unittest.main()

class ShippedPolicy(unittest.TestCase):
    def test_repo_policy_tiers(self):
        policy = (Path(__file__).resolve().parents[2] / '.env.ci').read_text(encoding='utf-8')
        self.assertEqual(select(policy, 'feat(app): wide surface', 'feature/wide'), 1200)
        self.assertEqual(select(policy, 'fix(store): replay text', 'fix/replay'), 600)
        self.assertEqual(select(policy, 'docs(adr): notes', 'docs/notes'), 2000)

    def test_repo_files_policy_tiers(self):
        policy = (Path(__file__).resolve().parents[2] / '.env.ci').read_text(encoding='utf-8')
        self.assertEqual(select_files(policy, 'feat(app): wide surface', 'feature/wide'), 15)
        self.assertEqual(select_files(policy, 'fix(store): replay text', 'fix/replay'), 10)
        self.assertEqual(select_files(policy, 'docs(adr): notes', 'docs/notes'), 10)
