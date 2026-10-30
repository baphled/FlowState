"""Exercise actual committed diffs, including common size-count bypasses."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / 'scripts/pr_size.py'


class PRSize(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git('init', '-q')
        self.git('config', 'user.name', 'Synthetic Fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.git('commit', '--allow-empty', '-qm', 'fixture base')
        self.base = self.git('rev-parse', 'HEAD').strip()

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, text=True)

    def write(self, path, data):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data if isinstance(data, bytes) else data.encode())

    def check(self, expected, base=None, extra=(), stage=True):
        if stage:
            self.git('add', '-A')
        self.git('commit', '--allow-empty', '-qm', 'fixture candidate')
        result = subprocess.run([sys.executable, '-B', str(SCRIPT), '--base',
                                 base or self.base, '--head', self.git('rev-parse', 'HEAD').strip(),
                                 *extra], cwd=self.root, capture_output=True, text=True,
                                env=dict(os.environ, MAX_LINES='999999'))
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        return result

    def test_exact_line_boundary_passes(self):
        self.write('source.go', 'line\n' * 500)
        self.check(0)

    def test_one_extra_line_fails_even_with_environment_override(self):
        self.write('source.go', 'line\n' * 501)
        self.check(1)

    def test_exact_file_boundary_passes(self):
        for number in range(10):
            self.write(str(number), '')
        self.check(0)

    def test_empty_files_still_count(self):
        for number in range(11):
            self.write(str(number), '')
        self.check(1)

    def test_additions_and_deletions_are_summed(self):
        self.write('source.go', 'old\n' * 251)
        self.git('add', '-A'); self.git('commit', '-qm', 'fixture old content')
        self.base = self.git('rev-parse', 'HEAD').strip()
        self.write('source.go', 'new\n' * 251)
        self.check(1)

    def test_test_docs_lockfiles_and_attributes_cannot_hide_lines(self):
        for path in ('unit_test.go', 'guide.md', 'go.sum', 'deps.lock'):
            self.write(path, 'line\n' * 126)
        self.write('.gitattributes', '* -diff\n')
        self.check(1)

    def test_renames_count_as_removed_and_added_review_content(self):
        self.write('before.go', 'line\n' * 251)
        self.git('add', '-A'); self.git('commit', '-qm', 'fixture before move')
        self.base = self.git('rev-parse', 'HEAD').strip()
        (self.root / 'before.go').rename(self.root / 'after.go')
        self.check(1)

    def test_binary_boundary_passes(self):
        self.write('fixture.bin', b'\0' * 131072)
        self.check(0)

    def test_binary_overflow_fails(self):
        self.write('fixture.bin', b'\0' * 131073)
        self.check(1)

    def test_large_blob_fails_closed(self):
        self.write('one-line.txt', b'x' * 1048577)
        self.check(1)

    def test_minified_text_cannot_hide_behind_one_line(self):
        self.write('minified.js', b'x' * 131073)
        self.check(1)

    def test_divergent_target_requires_refresh(self):
        self.git('checkout', '-qb', 'other')
        self.git('commit', '--allow-empty', '-qm', 'fixture alternate target')
        other = self.git('rev-parse', 'HEAD').strip()
        self.git('checkout', '--detach', self.base)
        self.check(1, base=other)

    def test_gitlink_cannot_be_hidden_as_zero_lines(self):
        self.git('update-index', '--add', '--cacheinfo', '160000', self.base, 'submodule')
        self.check(1, stage=False)

    def test_candidate_submodule_ignore_cannot_hide_gitlink(self):
        self.write('.gitmodules', '[submodule "nested"]\n path = nested\n ignore = all\n')
        self.git('add', '.gitmodules')
        self.git('update-index', '--add', '--cacheinfo', '160000', self.base, 'nested')
        self.check(1, stage=False)

    def test_local_submodule_ignore_cannot_hide_gitlink(self):
        self.git('config', 'diff.ignoreSubmodules', 'all')
        self.git('update-index', '--add', '--cacheinfo', '160000', self.base, 'nested')
        self.check(1, stage=False)

    def test_unusual_filename_is_not_ignored(self):
        self.write('space and\nnewline.go', 'line\n' * 501)
        self.check(1)

    def test_unresolved_base_fails_closed(self):
        self.check(1, base='f' * 40)

    def test_ref_option_injection_fails_closed(self):
        self.check(2, base='--all')

    def test_no_command_line_limit_override(self):
        self.check(2, extra=('--max-lines', '999999'))

    def test_build_requires_scope_gate_in_both_workflows(self):
        for name in ('ci-next.yml', 'ci.yml'):
            source = (ROOT / '.github/workflows' / name).read_text()
            build = source.split('  build:\n', 1)[1].split('\n  security:', 1)[0]
            with self.subTest(workflow=name):
                self.assertIn('scope', build.split('needs: [', 1)[1].split(']', 1)[0])


if __name__ == '__main__':
    unittest.main()
