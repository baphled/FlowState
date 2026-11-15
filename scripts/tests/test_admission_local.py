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
