"""Preflight rejects real admission failure classes without publishing a status."""
import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from scripts.admission_metadata import AdmissionError
from scripts.admission_preflight import main, ready_preflight
from scripts.tests.test_admission_io import (
    HEAD, PARENT, REVIEW_PRIVATE, EXECUTION_PRIVATE, FakeDropbox, FakeGitHub, public, pr,
)


class Readiness(unittest.TestCase):
    def ready(self, github=None, records=None, clock=lambda: 150):
        return ready_preflight(225, PARENT, HEAD, github or FakeGitHub(), records or FakeDropbox(),
                               public(REVIEW_PRIVATE), public(EXECUTION_PRIVATE), clock)

    def test_valid_signed_review_and_green_build_pass_without_publisher(self):
        github = FakeGitHub()
        self.ready(github)
        self.assertEqual(github.statuses, [])

    def test_missing_review_reference_and_wrong_local_base_fail(self):
        for changes in [{'body': ''}, {'base': {'ref': 'next', 'sha': 'f' * 40}}]:
            github = FakeGitHub()
            github.pr = lambda _: pr(**changes)
            with self.subTest(changes=changes):
                with self.assertRaises(AdmissionError):
                    self.ready(github)

    def test_missing_failed_and_skipped_build_fail(self):
        for verdict in ('missing', 'failure', 'skipped'):
            github = FakeGitHub()
            github.checks = [] if verdict == 'missing' else [dict(github.checks[0], conclusion=verdict)]
            with self.subTest(verdict=verdict):
                with self.assertRaisesRegex(AdmissionError, 'required check Build'):
                    self.ready(github)

    def test_changed_head_and_expiring_review_fail(self):
        with self.assertRaisesRegex(AdmissionError, 'state changed'):
            self.ready(FakeGitHub(change_head=True))
        times = iter([150, 200])
        with self.assertRaisesRegex(AdmissionError, 'stale'):
            self.ready(clock=lambda: next(times))

    def test_missing_credentials_and_ready_cache_fail_closed(self):
        args = ['--base', PARENT, '--head', HEAD]
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(main(args), 1)
            self.assertEqual(main(args + ['--phase', 'ready', '--pr', '225', '--records', '/cache']), 1)

    def test_unsigned_execution_fails(self):
        records = FakeDropbox()
        original = records.record
        def unsigned(reference):
            result = original(reference)
            result.pop('attestation', None)
            return result
        records.record = unsigned
        with self.assertRaisesRegex(AdmissionError, 'signature'):
            self.ready(records=records)


class PushHook(unittest.TestCase):
    def test_checks_each_pushed_tip_and_propagates_failure(self):
        hook = Path(__file__).resolve().parents[2] / '.githooks/pre-push'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            programs = {'git': '#!/bin/sh\ncase "$1" in rev-parse) echo ' + PARENT + ';; esac\n',
                        'python3': '#!/bin/sh\nexit 0\n',
                        'make': '#!/bin/sh\necho "$*" >> "$LOG"\nexit "${FAILURE:-0}"\n'}
            for name, source in programs.items():
                path = root / name
                path.write_text(source)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + ':' + os.environ['PATH'], LOG=str(root / 'log'))
            tips = f'refs/heads/other {HEAD} refs/heads/other {PARENT}\n'
            tips += f'refs/heads/second {PARENT} refs/heads/second {HEAD}\n'
            result = subprocess.run(['/bin/sh', str(hook)], input=tips, text=True, env=env)
            self.assertEqual(result.returncode, 0)
            calls = (root / 'log').read_text()
            self.assertIn('HEAD_SHA=' + HEAD, calls)
            self.assertIn('HEAD_SHA=' + PARENT, calls)
            result = subprocess.run(['/bin/sh', str(hook)], input=tips, text=True,
                                    env=dict(env, FAILURE='7'))
            self.assertEqual(result.returncode, 7)

    def test_warns_for_every_pushed_tip_that_is_not_head_but_checks_only_branches(self):
        hook = Path(__file__).resolve().parents[2] / '.githooks/pre-push'
        zero = '0' * 40
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            programs = {'git': '#!/bin/sh\ncase "$1" in rev-parse) echo ' + PARENT + ';; esac\n',
                        'python3': '#!/bin/sh\nexit 0\n',
                        'make': '#!/bin/sh\necho "$*" >> "$LOG"\n'}
            for name, source in programs.items():
                path = root / name
                path.write_text(source)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + ':' + os.environ['PATH'], LOG=str(root / 'log'))
            tips = (f'HEAD~1 {HEAD} refs/heads/raw {zero}\n'
                    f'{HEAD} {HEAD} refs/heads/sha {zero}\n'
                    f'refs/tags/v1 {HEAD} refs/tags/v1 {zero}\n'
                    f'(delete) {zero} refs/heads/gone {HEAD}\n'
                    f'refs/heads/head {PARENT} refs/heads/head {zero}\n'
                    f'refs/heads/other {HEAD} refs/tags/viaother {zero}\n')
            result = subprocess.run(['/bin/sh', str(hook)], input=tips, text=True, env=env,
                                    capture_output=True)
            self.assertEqual(result.returncode, 0)
            warnings = [line for line in result.stderr.splitlines() if 'warning' in line]
            self.assertEqual(len(warnings), 2)
            self.assertIn('pre-push: warning: HEAD~1 is not the checked-out HEAD; '
                          'the local CI preflight does not cover it', warnings)
            # Both branch tips are admission-checked, including the one pushed to a tag name.
            self.assertEqual((root / 'log').read_text().count('check-admission-commits'), 2)

    def test_failing_ci_preflight_aborts_the_push(self):
        hook = Path(__file__).resolve().parents[2] / '.githooks/pre-push'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            programs = {'git': '#!/bin/sh\ncase "$1" in rev-parse) echo ' + PARENT + ';; esac\n',
                        'python3': '#!/bin/sh\nexit 0\n',
                        'make': '#!/bin/sh\necho "$*" >> "$LOG"\n'
                                'case "$1" in preflight) exit 9;; esac\nexit 0\n'}
            for name, source in programs.items():
                path = root / name
                path.write_text(source)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + ':' + os.environ['PATH'], LOG=str(root / 'log'))
            tips = f'refs/heads/other {PARENT} refs/heads/other {HEAD}\n'
            result = subprocess.run(['/bin/sh', str(hook)], input=tips, text=True, env=env)
            self.assertEqual(result.returncode, 9)
            self.assertIn('preflight BASE_SHA=' + PARENT, (root / 'log').read_text())


class ToolPins(unittest.TestCase):
    """Makefile `make preflight` tool pins must match the versions CI installs."""
    root = Path(__file__).resolve().parents[2]
    pattern = {'golangci-lint': r'golangci-lint[^\n]*?(v\d+\.\d+\.\d+)|^\s*version: (v2\.\d+\.\d+)',
               'gosec': r'GOSEC_VERSION:?=? ?"?(v\d+\.\d+\.\d+)|gosec/v2/cmd/gosec@(v\d+\.\d+\.\d+)'}

    def versions(self, name, tool):
        text = (self.root / name).read_text()
        found = {m for groups in re.findall(self.pattern[tool], text, re.M) for m in groups if m}
        return found

    def test_makefile_pins_match_every_workflow_pin(self):
        makefile = {'golangci-lint': re.search(r'^GOLANGCI_LINT_VERSION := (\S+)', (self.root / 'Makefile').read_text(), re.M),
                    'gosec': re.search(r'^GOSEC_VERSION := (\S+)', (self.root / 'Makefile').read_text(), re.M)}
        for tool, match in makefile.items():
            self.assertIsNotNone(match, tool)
            pinned = set()
            for workflow in ('ci-next.yml', 'ci.yml', 'release.yml'):
                found = self.versions('.github/workflows/' + workflow, tool)
                pinned |= found
                with self.subTest(tool=tool, workflow=workflow):
                    self.assertLessEqual(found, {match.group(1)})
                    self.assertFalse(self.floating(workflow, tool), 'unpinned ' + tool + ' in ' + workflow)
            self.assertEqual(pinned, {match.group(1)}, tool + ' pin not found in any workflow')

    def floating(self, workflow, tool):
        """Lines that install the tool at a moving version such as latest or master."""
        text = (self.root / '.github/workflows' / workflow).read_text()
        loose = r'(@|bin |version: |VERSION: ?"?)(latest|master|main)\b'
        return [line for line in text.splitlines()
                if re.search(loose, line) and (tool in line or 'version:' in line or tool.upper().replace('-', '_') in line)]
