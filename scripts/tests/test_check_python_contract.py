"""Negative controls for changed committed Python source and test ownership."""
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from scripts.check_python_contract import candidate_errors, source_violations

ROOT = Path(__file__).resolve().parents[2]
GOOD = 'def task():\n    return 1\n'


class SourceContract(unittest.TestCase):
    def test_focused_function_is_accepted(self):
        self.assertEqual(source_violations('scripts/task.py', GOOD), [])

    def test_large_function_is_rejected(self):
        source = 'def task():\n' + '    pass\n' * 60
        self.assertTrue(any('lines' in x for x in source_violations('scripts/task.py', source)))

    def test_excessive_statements_are_rejected(self):
        source = 'def task():\n' + '    pass\n' * 41
        self.assertTrue(any('statements' in x for x in source_violations('scripts/task.py', source)))

    def test_conditional_complexity_is_rejected(self):
        source = 'def task():\n' + '    if True: pass\n' * 16
        self.assertTrue(any('complexity' in x for x in source_violations('scripts/task.py', source)))

    def test_large_or_branchy_module_is_rejected(self):
        module = GOOD + '# blank\n' * 250
        self.assertTrue(any('module' in x for x in source_violations('scripts/task.py', module)))
        branchy = 'if True: pass\n' * 36
        self.assertTrue(any('module branches' in x for x in source_violations('scripts/task.py', branchy)))

    def test_inline_waiver_does_not_exempt_complexity(self):
        source = 'def task(): # noqa: complexity\n' + '    if True: pass\n' * 16
        self.assertTrue(source_violations('scripts/task.py', source))

    def test_invalid_syntax_fails(self):
        self.assertTrue(any('invalid Python syntax' in x for x in source_violations('scripts/task.py', 'def :')))


class CommittedCandidate(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        (self.directory / 'scripts/tests').mkdir(parents=True)
        for file in ('check_python_contract.py', 'contract_sources.py'):
            shutil.copy(ROOT / 'scripts' / file, self.directory / 'scripts' / file)
        (self.directory / 'scripts/tests/test_check_python_contract.py').write_text('# fixture\n')
        (self.directory / 'scripts/legacy.py').write_text('def old():\n' + '    pass\n' * 80)
        self.git('init', '-q')
        self.git('config', 'user.name', 'Synthetic checker')
        self.git('config', 'user.email', 'checker@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')
        self.commit()
        self.base = self.git('rev-parse', 'HEAD').strip()

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.directory,
                                       stderr=subprocess.STDOUT).decode()

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'synthetic contract fixture')

    def guard(self):
        return subprocess.run(['python3', '-B', '-m', 'scripts.check_python_contract',
                               '--base', self.base], cwd=self.directory,
                              capture_output=True, text=True)

    def test_unmodified_legacy_debt_is_not_blocking(self):
        self.assertEqual(self.guard().returncode, 0)

    def test_missing_owner_test_is_rejected(self):
        (self.directory / 'scripts/new.py').write_text(GOOD)
        self.commit()
        self.assertIn('missing owning test', self.guard().stderr)

    def test_changed_but_unsaved_worktree_cannot_hide_failure(self):
        (self.directory / 'scripts/new.py').write_text('def task():\n' + '    pass\n' * 61)
        (self.directory / 'scripts/tests/test_new.py').write_text('# test\n')
        self.commit()
        (self.directory / 'scripts/new.py').write_text(GOOD)
        self.assertIn('exceeds', self.guard().stderr)

    def test_matching_test_allows_source(self):
        (self.directory / 'scripts/new.py').write_text(GOOD)
        (self.directory / 'scripts/tests/test_new.py').write_text('# test\n')
        self.commit()
        self.assertEqual(self.guard().returncode, 0)

    def test_additional_orphan_test_file_is_rejected(self):
        (self.directory / 'scripts/tests/test_new_extra.py').write_text('# orphan\n')
        self.commit()
        self.assertIn('no matching implementation', self.guard().stderr)

    def test_symlink_test_file_cannot_pass(self):
        (self.directory / 'scripts/new.py').write_text(GOOD)
        (self.directory / 'scripts/tests/test_new.py').symlink_to('test_check_python_contract.py')
        self.commit()
        self.assertIn('invalid owning test', self.guard().stderr)


if __name__ == '__main__':
    unittest.main()
