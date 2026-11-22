"""Oracles: contract checkers run from the PR base, never from the pull request's own tree."""
from pathlib import Path
import os
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
WRAPPER = ROOT / 'scripts/base_checkers.py'
sys.path.insert(0, str(ROOT / 'scripts'))
import base_checkers  # noqa: E402

REJECT = 'import sys\nprint("base checker verdict")\nsys.exit(1)\n'
ACCEPT = 'import sys\nsys.exit(0)\n'
SEES_CANDIDATE = 'import subprocess, sys\nsys.stdout.write(subprocess.check_output(["git", "show", "HEAD:candidate.txt"]).decode())\n'
ENTRY = 'scripts/check_code_contract.py'


class BaseCheckers(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in base_checkers.CHECKER_FILES:
            self.write(name, ACCEPT)
        for tree in base_checkers.CHECKER_TREES:
            self.write(tree + '/main.go', 'package main\n')
        self.write(base_checkers.CONFIG_FILE, 'version: "2"\n')
        self.write('candidate.txt', 'candidate\n')

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.root, stderr=subprocess.STDOUT).decode().strip()

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def commit(self):
        self.git('add', '-A')
        self.git('commit', '-qm', 'fixture')
        return self.git('rev-parse', 'HEAD')

    def start(self, entry_source):
        subprocess.check_call(['git', 'init', '-q'], cwd=self.root)
        self.write(ENTRY, entry_source)
        for key, value in (('user.name', 'Fixture'), ('user.email', 'fixture@example.invalid'), ('commit.gpgsign', 'false')):
            self.git('config', key, value)
        return self.commit()

    def run_base(self, base, *command):
        return subprocess.run([sys.executable, '-B', str(WRAPPER), 'run', '--base', base, '--', *command],
                              cwd=self.root, capture_output=True, text=True)

    def test_rewritten_checker_does_not_weaken_its_own_review(self):
        base = self.start(REJECT)
        self.write(ENTRY, ACCEPT)
        self.commit()
        self.assertEqual(self.run_base(base, sys.executable, '-B', ENTRY).returncode, 1)

    def test_base_checker_output_is_what_the_pull_request_sees(self):
        base = self.start(REJECT)
        self.write(ENTRY, ACCEPT)
        self.commit()
        self.assertIn('base checker verdict', self.run_base(base, sys.executable, '-B', ENTRY).stdout)

    def test_checkout_checker_alone_would_pass(self):
        self.start(REJECT)
        self.write(ENTRY, ACCEPT)
        self.commit()
        self.assertEqual(subprocess.run([sys.executable, '-B', ENTRY], cwd=self.root).returncode, 0)

    def test_legitimate_checker_change_is_judged_by_the_old_checker(self):
        base = self.start(ACCEPT)
        self.write(ENTRY, REJECT)
        self.commit()
        self.assertEqual(self.run_base(base, sys.executable, '-B', ENTRY).returncode, 0)

    def test_new_checker_applies_from_the_next_pull_request(self):
        self.start(ACCEPT)
        self.write(ENTRY, REJECT)
        merged = self.commit()
        self.write('next.txt', 'next pull request\n')
        self.commit()
        self.assertEqual(self.run_base(merged, sys.executable, '-B', ENTRY).returncode, 1)

    def test_missing_base_checker_path_fails_closed(self):
        self.start(REJECT)
        self.git('rm', '-q', '-r', 'scripts/test-contract')
        base = self.commit()
        self.write('scripts/test-contract/main.go', 'package main\n')
        self.commit()
        self.assertEqual(self.run_base(base, sys.executable, '-B', ENTRY).returncode, 2)

    def test_base_that_is_not_an_ancestor_fails_closed(self):
        self.start(ACCEPT)
        self.git('checkout', '-q', '-b', 'sibling')
        self.write('sibling.txt', 'sibling\n')
        sibling = self.commit()
        self.git('checkout', '-q', '-')
        self.write('head.txt', 'head\n')
        self.commit()
        self.assertEqual(self.run_base(sibling, sys.executable, '-B', ENTRY).returncode, 2)

    def test_unknown_base_fails_closed(self):
        self.start(ACCEPT)
        self.assertEqual(self.run_base('0' * 40, sys.executable, '-B', ENTRY).returncode, 2)

    def test_non_sha_base_fails_closed(self):
        self.start(ACCEPT)
        self.assertEqual(self.run_base('HEAD', sys.executable, '-B', ENTRY).returncode, 2)

    def test_base_checker_reads_the_candidate_commit(self):
        base = self.start(SEES_CANDIDATE)
        self.write('candidate.txt', 'changed candidate\n')
        self.commit()
        self.assertEqual(self.run_base(base, sys.executable, '-B', ENTRY).stdout, 'changed candidate\n')

    def test_linter_config_comes_from_the_base(self):
        base = self.start(ACCEPT)
        self.write(base_checkers.CONFIG_FILE, 'version: "2"\nlinters: {}\n')
        self.commit()
        dest = self.root.parent / 'exported.yml'
        self.addCleanup(lambda: dest.unlink(missing_ok=True))
        subprocess.check_call([sys.executable, '-B', str(WRAPPER), 'config', '--base', base, '--dest', str(dest)], cwd=self.root)
        self.assertEqual(dest.read_text(), 'version: "2"\n')

    def test_missing_base_linter_config_fails_closed(self):
        self.start(ACCEPT)
        self.git('rm', '-q', base_checkers.CONFIG_FILE)
        base = self.commit()
        self.write(base_checkers.CONFIG_FILE, 'version: "2"\n')
        self.commit()
        result = subprocess.run([sys.executable, '-B', str(WRAPPER), 'config', '--base', base, '--dest', str(self.root.parent / 'never.yml')],
                                cwd=self.root, capture_output=True)
        self.assertEqual(result.returncode, 2)

    def test_a_destination_the_linter_could_not_read_is_refused(self):
        base = self.start(ACCEPT)
        result = subprocess.run([sys.executable, '-B', str(WRAPPER), 'config', '--base', base, '--dest',
                                 str(self.root.parent / 'config-without-extension')],
                                cwd=self.root, capture_output=True)
        self.assertEqual(result.returncode, 2)

    def config(self, base, dest):
        return subprocess.run([sys.executable, '-B', str(WRAPPER), 'config', '--base', base, '--dest', str(dest)],
                              cwd=self.root, capture_output=True, text=True)

    def test_config_for_a_non_sha_base_fails_closed(self):
        self.start(ACCEPT)
        self.assertEqual(self.config('HEAD', self.root.parent / 'x.yml').returncode, 2)

    def test_config_for_a_base_that_is_not_an_ancestor_fails_closed(self):
        self.start(ACCEPT)
        self.git('checkout', '-q', '-b', 'sibling')
        self.write('sibling.txt', 'sibling\n')
        sibling = self.commit()
        self.git('checkout', '-q', '-')
        self.write('head.txt', 'head\n')
        self.commit()
        self.assertEqual(self.config(sibling, self.root.parent / 'x.yml').returncode, 2)

    def test_a_symlink_at_a_checker_path_in_the_base_fails_closed(self):
        self.start(ACCEPT)
        (self.root / 'scripts' / 'check_python_contract.py').unlink(missing_ok=True)
        (self.root / 'scripts' / 'check_python_contract.py').symlink_to('check_code_contract.py')
        base = self.commit()
        self.write('head.txt', 'head\n')
        self.commit()
        self.assertEqual(self.run_base(base, sys.executable, '-B', ENTRY).returncode, 2)

    def test_a_command_that_does_not_exist_fails_closed_with_exit_two(self):
        base = self.start(ACCEPT)
        self.assertEqual(self.run_base(base, 'no-such-command-for-the-base-checkers').returncode, 2)

    def test_a_destination_in_a_missing_directory_fails_closed_with_exit_two(self):
        base = self.start(ACCEPT)
        self.assertEqual(self.config(base, self.root.parent / 'missing-dir' / 'x.yml').returncode, 2)

    def test_the_base_copy_does_not_inherit_a_python_path_into_the_tree(self):
        base = self.start(ACCEPT)
        self.write('scripts/check_code_contract.py', 'raise SystemExit(0)\n')
        self.commit()
        env = dict(os.environ, PYTHONPATH=str(self.root))
        result = subprocess.run([sys.executable, '-B', str(WRAPPER), 'run', '--base', base, '--', sys.executable, '-B',
                                 '-c', 'import os,sys; sys.exit(0 if "PYTHONPATH" not in os.environ else 3)'],
                                cwd=self.root, env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0)

    def child_environment(self, *names):
        """What the command run from the base copy sees for each of the named variables."""
        base = self.start(ACCEPT)
        probe = 'import os; print("|".join(str(os.environ.get(n)) for n in %r))' % (names,)
        result = self.run_base(base, sys.executable, '-B', '-c', probe)
        self.assertEqual(result.returncode, 0)
        return dict(zip(names, result.stdout.strip().split('|')))

    def test_the_base_copy_does_not_use_a_go_workspace(self):
        self.assertEqual(self.child_environment('GOWORK')['GOWORK'], 'off')

    def test_the_base_copy_ignores_replacement_refs(self):
        self.assertEqual(self.child_environment('GIT_NO_REPLACE_OBJECTS')['GIT_NO_REPLACE_OBJECTS'], '1')

    def test_the_base_copy_builds_read_only(self):
        self.assertIn('-mod=readonly', self.child_environment('GOFLAGS')['GOFLAGS'])

    def test_a_replacement_ref_cannot_change_what_is_read_from_the_base(self):
        base = self.start(REJECT)
        self.write(ENTRY, ACCEPT)
        forged = self.commit()
        self.git('replace', base, forged)
        self.write('head.txt', 'head\n')
        self.commit()
        self.assertEqual(self.run_base(base, sys.executable, '-B', ENTRY).returncode, 1)


if __name__ == '__main__':
    unittest.main()
