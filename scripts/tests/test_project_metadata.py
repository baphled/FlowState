"""Real Git/CLI controls for the neutral external metadata contract."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
GUARD = ROOT / 'scripts/project_metadata.py'
HELPER = ROOT / 'scripts/ai-commit.sh'
REF = 'dropbox:/FlowState/Metadata/attempts/synthetic-fixture/record.json'


class MetadataCLI(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='metadata space ')
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name)
        self.repo = self.home / 'repo'
        self.repo.mkdir()
        self.run_git('init', '-q')
        self.run_git('config', 'user.name', 'Native Author')
        self.run_git('config', 'user.email', 'fixture@example.invalid')
        self.run_git('commit', '--allow-empty', '-qm', 'chore: fixture base')
        self.base = self.run_git('rev-parse', 'HEAD').stdout.strip()
        self.msg = self.home / 'message with space.txt'
        self.msg.write_text('fix: synthetic fixture\n')
        self.record = self.home / 'record with space.json'
        self.value = dict(record_type='flowstate-project-metadata-v1', metadata_ref=REF,
                          base_sha=self.base, docs_pack_manifest_sha256='a' * 64,
                          attribution=dict.fromkeys(('agent', 'provider', 'model', 'harness', 'source'), 'unverified'),
                          dropbox_reads=[dict(path=p, sha256='b' * 64, file_id='synthetic') for p in (
                              '/FlowState/Dev/00-AGENT-READ-FIRST.md', '/FlowState/Dev/AGENTS.md',
                              '/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md',
                              '/FlowState/Metadata/ACTIVE.json')])
        self.write_record()
        (self.repo / 'file with space.txt').write_text('fixture\n')
        self.run_git('add', '.')

    def write_record(self):
        self.record.write_text(json.dumps(self.value))

    def run_git(self, *args):
        return subprocess.run(['git', *args], cwd=self.repo, text=True, capture_output=True, check=True)

    def helper(self, **env):
        return subprocess.run(['bash', str(HELPER), str(self.msg), str(self.record)],
                              cwd=self.repo, env={**os.environ, **env}, text=True, capture_output=True)

    def test_override_does_not_enter_commit(self):
        result = self.helper(AI_AGENT='Invented', AI_MODEL='Invented', TMPDIR=str(self.home))
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertEqual(self.run_git('log', '-1', '--format=%B').stdout.strip(),
                         'fix: synthetic fixture\n\nProject-Metadata: ' + REF)

    def test_native_git_identity_does_not_fabricate_review(self):
        result = self.helper(AI_AGENT='Invented', AI_MODEL='Invented')
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertNotIn('Reviewed-By:', self.run_git('log', '-1', '--format=%B').stdout)

    def test_failed_hook_cleans_temporary_message_and_preserves_input(self):
        hook = self.repo / '.git/hooks/pre-commit'
        hook.write_text('#!/bin/sh\nexit 1\n')
        hook.chmod(0o755)
        before = set(self.home.iterdir())
        self.assertNotEqual(self.helper(AI_AGENT='x', AI_MODEL='y', TMPDIR=str(self.home)).returncode, 0)
        self.assertEqual(set(self.home.iterdir()), before)
        self.assertEqual(self.msg.read_text(), 'fix: synthetic fixture\n')
        self.assertEqual(self.run_git('rev-parse', 'HEAD').stdout.strip(), self.base)

    def test_foreign_record_is_rejected(self):
        self.value['base_sha'] = '0' * 40
        self.write_record()
        self.assertNotEqual(self.helper(AI_AGENT='x', AI_MODEL='y').returncode, 0)
        self.assertEqual(self.run_git('rev-parse', 'HEAD').stdout.strip(), self.base)

    def test_mismatched_reference_is_rejected(self):
        self.msg.write_text('fix: fixture\n\nProject-Metadata: ' + REF.replace('record.json', 'foreign.json'))
        self.assertNotEqual(self.helper(AI_AGENT='x', AI_MODEL='y').returncode, 0)

    def test_no_verify_environment_cannot_bypass_hook(self):
        hook = self.repo / '.git/hooks/pre-commit'
        hook.write_text('#!/bin/sh\nexit 1\n')
        hook.chmod(0o755)
        self.assertNotEqual(self.helper(NO_VERIFY='1', AI_AGENT='x', AI_MODEL='y').returncode, 0)

    def test_namespaced_observed_reads_are_supported(self):
        for read in self.value['dropbox_reads']:
            read['path'] = 'ns:12345/' + read['path']
        self.write_record()
        result = self.helper()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_in_checkout_record_is_rejected(self):
        self.record = self.repo / 'cache.json'
        self.write_record()
        self.assertNotEqual(self.helper().returncode, 0)

    def test_valid_non_go_range_with_space_path_passes(self):
        self.run_git('commit', '-qm', 'docs: fixture\n\nProject-Metadata: ' + REF)
        result = subprocess.run(['python3', str(GUARD), '--changed', '--base', self.base], cwd=self.repo, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_all_forbidden_trailers_are_rejected(self):
        for claim in ('AI-Generated-By', 'Reviewed-By', 'Co-authored-by', 'AI-Agent', 'AI-Model'):
            with self.subTest(claim=claim):
                self.msg.write_text('fix: fixture\n\nProject-Metadata: ' + REF + '\n' + claim + ': invented\n')
                result = subprocess.run(['python3', str(GUARD), '--message-file', str(self.msg)], cwd=self.repo, capture_output=True)
                self.assertNotEqual(result.returncode, 0)

    def test_range_rejects_bad_non_go_commit_even_when_later_deleted(self):
        self.run_git('commit', '-qm', 'docs: missing reference')
        self.run_git('rm', 'file with space.txt')
        self.run_git('commit', '-qm', 'docs: remove\n\nProject-Metadata: ' + REF)
        result = subprocess.run(['python3', str(GUARD), '--changed', '--base', self.base], cwd=self.repo, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b'Project-Metadata', result.stderr)

    def test_range_checks_metadata_path_with_spaces(self):
        folder = self.repo / 'reports'
        folder.mkdir()
        (folder / 'actual run.txt').write_text('captured result')
        self.run_git('add', '.')
        self.run_git('commit', '-qm', 'docs: fixture\n\nProject-Metadata: ' + REF)
        result = subprocess.run(['python3', str(GUARD), '--changed', '--base', self.base], cwd=self.repo, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(b'reports/actual run.txt', result.stderr)


if __name__ == '__main__':
    unittest.main()
