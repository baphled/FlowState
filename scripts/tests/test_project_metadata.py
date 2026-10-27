import importlib.util
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location('metadata', ROOT / 'scripts/project_metadata.py')
metadata = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(metadata)
REF = 'dropbox:/FlowState/Metadata/attempts/test-attempt/record.json'


class MetadataTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / 'repo'
        self.repo.mkdir()
        self.env = dict(os.environ, GIT_CONFIG_NOSYSTEM='1',
                        GIT_AUTHOR_NAME='fixture', GIT_AUTHOR_EMAIL='fixture@example.invalid',
                        GIT_COMMITTER_NAME='fixture', GIT_COMMITTER_EMAIL='fixture@example.invalid',
                        PYTHONDONTWRITEBYTECODE='1')
        for key in ('GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GIT_COMMON_DIR'):
            self.env.pop(key, None)
        self.run_git('init', '-b', 'feature/test')
        self.run_git('config', 'user.name', 'Not a Reviewer')
        self.run_git('config', 'user.email', 'fixture@example.invalid')
        self.write('main.go', 'package main\n')
        self.run_git('add', '.')
        self.run_git('commit', '-qm', 'chore: fixture base')
        self.base = self.run_git('rev-parse', 'HEAD').stdout.strip()

    def run_git(self, *args):
        return subprocess.run(['git', *args], cwd=self.repo, env=self.env,
                              text=True, capture_output=True, check=True)

    def write(self, path, text):
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)

    def guard(self, *args):
        return subprocess.run(['python3', '-B', str(ROOT / 'scripts/project_metadata.py'), *args],
                              cwd=self.repo, env=self.env, text=True, capture_output=True)

    def record(self, dest=None):
        dest = dest or self.root / 'record.json'
        value = {'record_type': 'flowstate-project-metadata-v1', 'metadata_ref': REF,
                 'base_sha': self.base, 'docs_pack_manifest_sha256': 'a' * 64,
                 'attribution': {'agent': 'fixture', 'provider': 'unverified', 'model': 'unverified',
                                 'harness': 'fixture', 'source': 'synthetic test'},
                 'dropbox_reads': [{'path': p, 'sha256': 'a' * 64, 'file_id': 'fixture-only'}
                                   for p in ['/FlowState/Dev/00-AGENT-READ-FIRST.md',
                                             '/FlowState/Dev/AGENTS.md',
                                             '/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md',
                                             '/FlowState/Metadata/ACTIVE.json']]}
        dest.write_text(json.dumps(value))
        return dest

    def test_rejects_qualification_report_in_index_even_after_working_copy_deleted(self):
        self.write('docs/T019-qualification.md', '# Actual run report')
        self.run_git('add', '.')
        (self.repo / 'docs/T019-qualification.md').unlink()
        self.assertNotEqual(self.guard('--staged').returncode, 0)

    def test_rejects_receipt_disguised_under_unrelated_name(self):
        self.write('docs/ready.json', json.dumps({'schema_version': 'm1-acceptance-receipt-v2'}))
        self.run_git('add', '.')
        self.assertNotEqual(self.guard('--staged').returncode, 0)

    def test_rejects_captured_evidence_directory(self):
        self.write('evidence/run.txt', 'actual output')
        self.run_git('add', '.')
        self.assertNotEqual(self.guard('--staged').returncode, 0)

    def test_allows_qualification_test_source_and_schema(self):
        self.write('internal/storage/qualification_test.go', 'package storage\n')
        self.write('contracts/receipt.schema.json', '{"type":"object"}')
        self.run_git('add', '.')
        self.assertEqual(self.guard('--staged').returncode, 0)

    def test_allows_explicit_synthetic_fixture_only(self):
        self.write('internal/storage/testdata/receipt.json', '{"synthetic_fixture":true}')
        self.run_git('add', '.')
        self.assertEqual(self.guard('--staged').returncode, 0)
        self.write('internal/storage/testdata/receipt.json', '{"gate_outcome":"pass"}')
        self.run_git('add', '.')
        self.assertNotEqual(self.guard('--staged').returncode, 0)

    def test_neutral_reference_valid_but_identity_trailers_rejected(self):
        good = 'chore(tools): change\n\nProject-Metadata: ' + REF + '\n'
        self.assertEqual(metadata.message_errors(good), [])
        for trailer in ['AI-Generated-By: guessed', 'Reviewed-By: Git User', 'Co-authored-by: guessed']:
            self.assertTrue(metadata.message_errors(good + trailer))
        self.assertTrue(metadata.message_errors('chore: missing ref'))
        self.assertTrue(metadata.message_errors(good.replace('dropbox:/FlowState/Metadata', '/tmp')))

    def test_range_rejects_bad_attribution_commit(self):
        self.write('main.go', 'package main\n// change\n')
        self.run_git('add', '.')
        self.run_git('commit', '-qm', 'chore: fixture\n\nAI-Generated-By: guessed')
        self.assertNotEqual(self.guard('--base', self.base).returncode, 0)

    def test_range_rejects_record_added_then_deleted(self):
        self.write('evidence/qualification.json', '{"result":"pass"}')
        self.run_git('add', '.')
        message = 'chore: fixture change\n\nProject-Metadata: ' + REF
        self.run_git('commit', '-qm', message)
        self.run_git('rm', 'evidence/qualification.json')
        self.run_git('commit', '-qm', message)
        self.assertNotEqual(self.guard('--base', self.base).returncode, 0)

    def test_rejects_repository_handoff_and_task_records(self):
        for path in ['HANDOFF.md', 'docs/PLAN.md', 'tasks/next.md']:
            self.assertTrue(metadata.record_errors(path, b'project record'))

    def test_rejects_cucumber_files_dependencies_imports_and_workflow(self):
        cases = [('features/new.feature', b'Feature: prohibited'),
                 ('go.mod', b'require github.com/cucumber/godog v0.16.0'),
                 ('internal/run_test.go', b'import "github.com/cucumber/godog"'),
                 ('.github/workflows/test.yml', b'run: make bdd-smoke'),
                 ('.github/workflows/one.yml', b'- run: godog\n- run: go test ./...'),
                 ('.github/workflows/two.yml', b'run: |\n  godog\n  go test ./...')]
        for path, content in cases:
            self.assertTrue(metadata.record_errors(path, content), path)

    def test_external_record_requires_actual_base_and_read_set(self):
        path = self.record()
        self.assertEqual(self.guard('--external-record', str(path)).returncode, 0)
        value = json.loads(path.read_text())
        value['base_sha'] = 'b' * 40
        path.write_text(json.dumps(value))
        self.assertNotEqual(self.guard('--external-record', str(path)).returncode, 0)
        value['base_sha'] = self.base
        value['dropbox_reads'] = []
        path.write_text(json.dumps(value))
        self.assertNotEqual(self.guard('--external-record', str(path)).returncode, 0)

    def test_record_cache_inside_checkout_is_rejected(self):
        path = self.record(self.repo / 'record.json')
        self.assertNotEqual(self.guard('--external-record', str(path)).returncode, 0)


if __name__ == '__main__':
    unittest.main()
