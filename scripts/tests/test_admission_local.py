"""Exercise immutable local commit manifests and external caches in isolated Git repos."""
import hashlib
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

from scripts.admission_local import CachedRecords, LocalGit, local_commits
from scripts.admission_metadata import AdmissionError
from scripts.admission_preflight import commit_preflight
from scripts.admission_candidate import verify_files
from scripts.tests.test_admission_changes import DELETED, MODE_CASES, NEW, OLD, SCENARIOS
from scripts.tests.test_admission_changes import attested, blob_entry, manifest_cases
from scripts.tests.test_admission_io import REF, record


class LocalEvidence(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / 'repo'
        self.repo.mkdir()
        previous = os.getcwd()
        os.chdir(self.repo)
        self.addCleanup(os.chdir, previous)
        self.git('init', '-q')
        self.git('config', 'user.name', 'Synthetic test fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')
        self.git('commit', '--allow-empty', '-qm', 'chore: synthetic base')
        self.base = self.git('rev-parse', 'HEAD').strip()
        Path('demo').write_bytes(b'demo')
        self.git('add', 'demo')
        self.git('commit', '-qm', 'chore: synthetic candidate\n\nProject-Metadata: ' + REF)
        self.head = self.git('rev-parse', 'HEAD').strip()
        self.evidence = record(base_sha=self.base)
        self.evidence['local_candidate']['paths'] = {'demo': {
            'sha256': hashlib.sha256(b'demo').hexdigest(), 'bytes': 4}}

    def git(self, *args):
        return subprocess.check_output(['git', *args], stderr=subprocess.PIPE).decode()

    def cache(self, evidence=None):
        path = self.root / 'records.json'
        path.write_text(json.dumps({REF: self.evidence if evidence is None else evidence}))
        return CachedRecords(path)

    def test_actual_committed_content_passes_even_if_worktree_changes(self):
        Path('demo').write_text('uncommitted')
        self.assertIsNone(commit_preflight(self.base, self.head, self.cache()))

    def test_rebased_metadata_parent_is_rejected(self):
        bad = dict(self.evidence, base_sha='f' * 40)
        with self.assertRaisesRegex(AdmissionError, 'base SHA'):
            commit_preflight(self.base, self.head, self.cache(bad))

    def test_wrong_blob_and_missing_cache_record_are_rejected(self):
        self.evidence['local_candidate']['paths']['demo']['bytes'] = 99
        with self.assertRaisesRegex(AdmissionError, 'blob does not match'):
            commit_preflight(self.base, self.head, self.cache())
        with self.assertRaisesRegex(AdmissionError, 'missing'):
            self.cache().record(REF.replace('commit.json', 'absent.json'))

    def test_cache_inside_checkout_and_duplicate_keys_are_rejected(self):
        inside = self.repo / 'records.json'
        inside.write_text('{}')
        with self.assertRaisesRegex(AdmissionError, 'outside'):
            CachedRecords(inside)
        outside = self.root / 'duplicate.json'
        outside.write_text('{"duplicate":1,"duplicate":2}')
        with self.assertRaisesRegex(AdmissionError, 'duplicate'):
            CachedRecords(outside)

    def test_empty_nonexact_and_nonancestor_ranges_fail(self):
        for base, head in [(self.base, self.base), ('HEAD', self.head), (self.head, self.base)]:
            with self.subTest(base=base, head=head):
                with self.assertRaises((AdmissionError, subprocess.CalledProcessError)):
                    local_commits(base, head)

    def test_symlink_blob_is_rejected(self):
        Path('link').symlink_to('demo')
        self.git('add', 'link')
        self.git('commit', '-qm', 'chore: synthetic symlink')
        with self.assertRaisesRegex(AdmissionError, 'regular'):
            LocalGit().commit_files(self.git('rev-parse', 'HEAD').strip())

    def test_complete_commit_range_is_read(self):
        self.git('commit', '--allow-empty', '-qm', 'chore: second synthetic commit')
        self.assertEqual(len(local_commits(self.base, self.git('rev-parse', 'HEAD').strip())), 2)


class LocalDeletions(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        previous = os.getcwd()
        os.chdir(self.repo)
        self.addCleanup(os.chdir, previous)
        self.git('init', '-q')
        self.git('config', 'user.name', 'Synthetic test fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')

    def git(self, *args):
        return subprocess.check_output(['git', *args], stderr=subprocess.PIPE).decode().strip()

    def commit_tree(self, files):
        for path in Path('.').iterdir():
            if path.name != '.git':
                path.unlink()
        for name, data in files.items():
            Path(name).write_bytes(data)
        self.git('add', '-A')
        self.git('commit', '--allow-empty', '-qm', 'chore: synthetic')
        return self.git('rev-parse', 'HEAD')

    def scenario_head(self, scenario):
        parent, child = SCENARIOS[scenario][:2]
        self.commit_tree(parent)
        return parent, child, self.commit_tree(child)

    def test_shared_manifest_table(self):
        for scenario, label, overrides, error in manifest_cases():
            with self.subTest(scenario=scenario, label=label):
                parent, child, head = self.scenario_head(scenario)
                manifest = attested(parent, child, overrides)
                if error is None:
                    verify_files(LocalGit(), head, manifest)
                else:
                    with self.assertRaisesRegex(AdmissionError, error):
                        verify_files(LocalGit(), head, manifest)

    def test_deleted_file_is_removed_without_blob(self):
        _, _, head = self.scenario_head('all-deleted')
        files = sorted(LocalGit().commit_files(head), key=lambda item: item['filename'])
        self.assertEqual(files, [{'filename': 'also', 'status': 'removed'},
                                 {'filename': 'gone', 'status': 'removed'}])

    def test_gitlink_is_rejected(self):
        self.commit_tree({'keep': b'k'})
        self.git('update-index', '--add', '--cacheinfo', '160000,' + 'a' * 40 + ',sub')
        self.git('commit', '-qm', 'chore: synthetic gitlink')
        with self.assertRaisesRegex(AdmissionError, 'regular'):
            LocalGit().commit_files(self.git('rev-parse', 'HEAD'))

    def test_file_to_symlink_type_change_is_rejected(self):
        self.commit_tree({'node': b'k'})
        Path('node').unlink()
        Path('node').symlink_to('elsewhere')
        self.git('add', '-A')
        self.git('commit', '-qm', 'chore: synthetic type change')
        with self.assertRaisesRegex(AdmissionError, 'regular'):
            LocalGit().commit_files(self.git('rev-parse', 'HEAD'))

    def test_symlink_to_file_type_change_is_rejected(self):
        self.commit_tree({})
        Path('node').symlink_to('elsewhere')
        self.git('add', '-A')
        self.git('commit', '-qm', 'chore: synthetic symlink')
        Path('node').unlink()
        Path('node').write_bytes(b'k')
        self.git('add', '-A')
        self.git('commit', '-qm', 'chore: synthetic type change')
        head = self.git('rev-parse', 'HEAD')
        self.assertEqual(LocalGit().commit_files(head)[0]['status'], 'unsupported')
        with self.assertRaisesRegex(AdmissionError, 'unsupported'):
            verify_files(LocalGit(), head, {'node': {'sha256': hashlib.sha256(b'k').hexdigest(), 'bytes': 1}})

    def stage(self, mode, data):
        blob = subprocess.run(['git', 'hash-object', '-w', '--stdin'], input=data,
                              stdout=subprocess.PIPE, check=True).stdout.decode().strip()
        self.git('update-index', '--add', '--cacheinfo', f'{mode},{blob},node')

    def test_shared_mode_table(self):
        for label, before, now, error in MODE_CASES:
            with self.subTest(label=label):
                self.commit_tree({})
                if before is not None:
                    self.stage(before, OLD)
                    self.git('commit', '-qm', 'chore: synthetic parent')
                self.stage(now, NEW)
                self.git('commit', '-qm', 'chore: synthetic child')
                manifest = {'node': blob_entry(NEW)}
                head = self.git('rev-parse', 'HEAD')
                if error is None:
                    verify_files(LocalGit(), head, manifest)
                else:
                    with self.assertRaisesRegex(AdmissionError, error):
                        verify_files(LocalGit(), head, manifest)

    def test_deleted_symlink_matches_github_removed(self):
        self.commit_tree({})
        Path('node').symlink_to('elsewhere')
        self.git('add', '-A')
        self.git('commit', '-qm', 'chore: synthetic symlink')
        Path('node').unlink()
        self.git('add', '-A')
        self.git('commit', '-qm', 'chore: synthetic removal')
        verify_files(LocalGit(), self.git('rev-parse', 'HEAD'), {'node': DELETED})
