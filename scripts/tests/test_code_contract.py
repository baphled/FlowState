"""Committed Git regression controls for production contract enforcement."""
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
BAD = 'package fixture\ntype record struct{}\n'
GOOD = 'package fixture\nfunc work(){}\n'


class CodeCandidate(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'scripts').mkdir()
        for name in ['contract_sources.py', 'check_code_contract.py']:
            shutil.copy(ROOT / 'scripts' / name, self.root / 'scripts')
        shutil.copytree(ROOT / 'scripts/code-contract', self.root / 'scripts/code-contract', ignore=shutil.ignore_patterns('*_test.go'))
        (self.root / 'go.mod').write_text('module fixture\n\ngo 1.26.0\n')
        (self.root / 'scripts/fixture').mkdir()
        (self.root / 'scripts/fixture/legacy.go').write_text(BAD)
        self.git('init', '-q')
        self.git('config', 'user.name', 'Synthetic contract fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')
        self.commit()
        self.base = self.git('rev-parse', 'HEAD').strip()

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, stderr=subprocess.STDOUT).decode()

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'synthetic contract fixture')

    def guard(self):
        return subprocess.run(['python3', '-B', 'scripts/check_code_contract.py', '--base', self.base], cwd=self.root, capture_output=True, text=True).returncode

    def test_untouched_debt_is_not_claimed_as_fixed(self):
        self.assertEqual(self.guard(), 0)

    def test_committed_violation_cannot_be_hidden_by_working_copy(self):
        (self.root / 'scripts/fixture/added.go').write_text(BAD)
        self.commit()
        (self.root / 'scripts/fixture/added.go').write_text(GOOD)
        self.assertNotEqual(self.guard(), 0)

    def test_deleted_working_copy_does_not_hide_committed_type(self):
        (self.root / 'scripts/fixture/added.go').write_text(BAD)
        self.commit()
        (self.root / 'scripts/fixture/added.go').unlink()
        self.assertNotEqual(self.guard(), 0)

    def test_valid_owned_source_passes(self):
        path = self.root / 'scripts/fixture/types.go'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(BAD)
        self.commit()
        self.assertEqual(self.guard(), 0)

    def test_rename_cannot_exempt_changed_source(self):
        self.git('mv', 'scripts/fixture/legacy.go', 'scripts/fixture/renamed.go')
        self.commit()
        self.assertNotEqual(self.guard(), 0)

    def test_symlink_mode_is_rejected(self):
        oid = subprocess.check_output(['git', 'hash-object', '-w', '--stdin'], cwd=self.root, input=GOOD.encode()).decode().strip()
        self.git('update-index', '--add', '--cacheinfo', '120000', oid, 'scripts/fixture/linked.go')
        self.git('commit', '-qm', 'synthetic contract link')
        self.assertNotEqual(self.guard(), 0)

    def test_platform_import_cannot_escape_domain(self):
        path = self.root / 'internal/runtime/deep/service_windows.go'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text('//go:build windows\n\npackage deep\nimport _ "net/http"\n')
        self.commit()
        self.assertNotEqual(self.guard(), 0)


if __name__ == '__main__':
    unittest.main()
