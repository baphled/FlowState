"""Real Git regressions for committed-candidate test enforcement."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
LEGACY = 'package fixture\nimport "testing"\nfunc TestLegacy(t *testing.T){}\n'
VALID = 'package fixture\nimport . "github.com/onsi/ginkgo/v2"\nimport . "github.com/onsi/gomega"\nvar _=It("works",func(){Expect(value).To(Equal(1))})\n'


class CandidateContract(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'scripts').mkdir()
        shutil.copy(ROOT / 'scripts/check_test_contract.py', self.root / 'scripts')
        shutil.copytree(ROOT / 'scripts/test-contract', self.root / 'scripts/test-contract', ignore=shutil.ignore_patterns('*_test.go'))
        (self.root / 'go.mod').write_text('module fixture\n\ngo 1.26.0\n')
        self.git('init', '-q')
        self.git('config', 'user.name', 'Synthetic test fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')
        (self.root / 'legacy_test.go').write_text(LEGACY)
        self.commit()
        self.base = self.git('rev-parse', 'HEAD').strip()

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, stderr=subprocess.STDOUT).decode()

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'synthetic fixture')

    def guard(self):
        return subprocess.run(['python3', '-B', 'scripts/check_test_contract.py', '--base', self.base], cwd=self.root, capture_output=True, text=True)

    def test_unchanged_legacy_is_allowed(self):
        self.assertEqual(self.guard().returncode, 0)

    def test_new_stdlib_test_is_rejected(self):
        (self.root / 'new_test.go').write_text(LEGACY)
        self.commit()
        self.assertNotEqual(self.guard().returncode, 0)

    def test_working_copy_cannot_mask_committed_violation(self):
        (self.root / 'new_test.go').write_text(LEGACY)
        self.commit()
        (self.root / 'new_test.go').write_text(VALID)
        self.assertNotEqual(self.guard().returncode, 0)

    def test_working_copy_deletion_cannot_mask_committed_violation(self):
        (self.root / 'new_test.go').write_text(LEGACY)
        self.commit()
        (self.root / 'new_test.go').unlink()
        self.assertNotEqual(self.guard().returncode, 0)

    def test_rename_is_not_an_exemption(self):
        self.git('mv', 'legacy_test.go', 'renamed_test.go')
        self.commit()
        self.assertNotEqual(self.guard().returncode, 0)

    def test_valid_changed_spec_is_allowed(self):
        (self.root / 'new_test.go').write_text(VALID)
        self.commit()
        self.assertEqual(self.guard().returncode, 0)

    def test_committed_vacuous_oracles_are_rejected(self):
        cases = (
            'Expect(err == nil || err != nil).To(BeTrue())',
            'Expect(x || !x).To(BeTrue())',
            'Expect([]any{err, count}).To(Equal(want))',
            'Expect([4]any{err, count, name, ok}).To(Equal(want))',
            'if err != nil { return }; Expect(value).To(Equal(1))',
        )
        for body in cases:
            with self.subTest(body=body):
                source = VALID.replace('Expect(value).To(Equal(1))', body)
                (self.root / 'new_test.go').write_text(source)
                self.commit()
                self.assertNotEqual(self.guard().returncode, 0)

    def test_symlink_mode_is_rejected_without_os_symlink_privileges(self):
        oid = subprocess.check_output(['git', 'hash-object', '-w', '--stdin'], cwd=self.root, input=VALID.encode()).decode().strip()
        self.git('update-index', '--add', '--cacheinfo', '120000', oid, 'linked_test.go')
        self.git('commit', '-qm', 'synthetic link fixture')
        self.assertNotEqual(self.guard().returncode, 0)


if __name__ == '__main__':
    unittest.main()
