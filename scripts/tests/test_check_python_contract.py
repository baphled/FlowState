"""Integration tests for committed Python source and test ownership."""
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from scripts.check_python_contract import candidate_errors

ROOT = Path(__file__).resolve().parents[2]
GOOD = 'def task():\n    return 1\n'
TEST = 'def test_task():\n    assert True\n'


class CommittedCandidate(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        (self.directory / 'scripts/tests').mkdir(parents=True)
        for file in ('check_python_contract.py', 'contract_sources.py', 'python_contract_ast.py'):
            shutil.copy(ROOT / 'scripts' / file, self.directory / 'scripts' / file)
        (self.directory / 'scripts/tests/test_check_python_contract.py').write_text('# fixture\n')
        (self.directory / 'scripts/tests/test_python_contract_ast.py').write_text('# fixture\n')
        (self.directory / 'scripts/legacy.py').write_text('def old():\n' + '    pass\n' * 80)
        (self.directory / 'scripts/existing.py').write_text(GOOD)
        (self.directory / 'scripts/tests/test_existing.py').write_text(TEST)
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
        self.git('add', '-A')
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

    def test_deleted_owner_test_is_rejected(self):
        (self.directory / 'scripts/tests/test_existing.py').unlink()
        self.commit()
        self.assertIn('missing owning test', self.guard().stderr)

    def test_renamed_owner_test_is_rejected(self):
        source = self.directory / 'scripts/tests/test_existing.py'
        source.rename(self.directory / 'scripts/tests/test_renamed.py')
        self.commit()
        result = self.guard()
        self.assertIn('missing owning test', result.stderr)
        self.assertIn('no matching implementation', result.stderr)

    def test_changed_but_unsaved_worktree_cannot_hide_failure(self):
        (self.directory / 'scripts/new.py').write_text('def task():\n' + '    pass\n' * 61)
        (self.directory / 'scripts/tests/test_new.py').write_text(TEST)
        self.commit()
        (self.directory / 'scripts/new.py').write_text(GOOD)
        self.assertIn('exceeds', self.guard().stderr)

    def test_matching_test_allows_source(self):
        (self.directory / 'scripts/new.py').write_text(GOOD)
        (self.directory / 'scripts/tests/test_new.py').write_text(TEST)
        self.commit()
        self.assertEqual(self.guard().returncode, 0)

    def test_nested_matching_test_allows_source(self):
        nested = self.directory / 'scripts/package'
        (nested / 'tests').mkdir(parents=True)
        (nested / 'worker.py').write_text(GOOD)
        (nested / 'tests/test_worker.py').write_text(TEST)
        self.commit()
        self.assertEqual(self.guard().returncode, 0)

    def test_additional_orphan_test_file_is_rejected(self):
        (self.directory / 'scripts/tests/test_new_extra.py').write_text(TEST)
        self.commit()
        self.assertIn('no matching implementation', self.guard().stderr)

    def test_nested_orphan_test_file_is_rejected(self):
        nested = self.directory / 'scripts/package/tests'
        nested.mkdir(parents=True)
        (nested / 'test_orphan.py').write_text(TEST)
        self.commit()
        self.assertIn('no matching implementation', self.guard().stderr)

    def test_deeply_nested_test_module_is_rejected(self):
        nested = self.directory / 'scripts/tests/helpers'
        nested.mkdir(parents=True)
        (nested / 'test_orphan.py').write_text(TEST)
        self.commit()
        self.assertIn('unsupported owning test layout', self.guard().stderr)

    def test_nested_test_function_is_not_discoverable(self):
        (self.directory / 'scripts/new.py').write_text(GOOD)
        hidden = 'def helper():\n    def test_fake():\n        assert True\n'
        (self.directory / 'scripts/tests/test_new.py').write_text(hidden)
        self.commit()
        self.assertIn('no discoverable test_*', self.guard().stderr)

    def test_unittest_case_method_is_discoverable(self):
        (self.directory / 'scripts/new.py').write_text(GOOD)
        test = ('import unittest\n\nclass NewTests(unittest.TestCase):\n'
                '    def test_task(self):\n        self.assertTrue(True)\n')
        (self.directory / 'scripts/tests/test_new.py').write_text(test)
        self.commit()
        self.assertEqual(self.guard().returncode, 0)

    def test_empty_test_file_is_rejected(self):
        (self.directory / 'scripts/new.py').write_text(GOOD)
        (self.directory / 'scripts/tests/test_new.py').write_text('# no tests\n')
        self.commit()
        self.assertIn('no discoverable test_*', self.guard().stderr)

    def test_symlink_implementation_cannot_pass(self):
        (self.directory / 'scripts/new.py').symlink_to('legacy.py')
        (self.directory / 'scripts/tests/test_new.py').write_text(TEST)
        self.commit()
        self.assertIn('invalid committed source', self.guard().stderr)

    def test_symlink_test_file_cannot_pass(self):
        (self.directory / 'scripts/new.py').write_text(GOOD)
        (self.directory / 'scripts/tests/test_new.py').symlink_to('test_check_python_contract.py')
        self.commit()
        self.assertIn('invalid owning test', self.guard().stderr)


if __name__ == '__main__':
    unittest.main()
