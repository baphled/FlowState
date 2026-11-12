"""Git tree/range walk oracles for the decomposed scan module."""
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))
from scripts.project_metadata.scan import changed_tree_errors, directory_errors, tree_errors

REF = 'dropbox:/FlowState/Metadata/attempts/synthetic-fixture/record.json'


def in_repo(repo, fn):
    previous = Path.cwd()
    os.chdir(repo)
    try:
        return fn()
    finally:
        os.chdir(previous)


class TreeWalks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name) / 'repo'
        self.repo.mkdir()
        self.git('init', '-q')
        self.git('commit', '--allow-empty', '-qm', 'chore: fixture base')
        self.base = self.git('rev-parse', 'HEAD').stdout.strip()

    def git(self, *args):
        return subprocess.run(['git', *args], cwd=self.repo, text=True,
                              capture_output=True, check=True)

    def commit(self, message, name=None):
        if name:
            target = self.repo / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(b'fixture\n')
            self.git('add', '.')
        self.git('commit', '-qm', message)

    def test_clean_range_and_index_pass(self):
        self.commit('docs: fixture\n\nProject-Metadata: ' + REF, 'ok.txt')
        self.assertEqual(in_repo(self.repo, lambda: changed_tree_errors(self.base, 'HEAD')), [])
        self.assertEqual(in_repo(self.repo, lambda: tree_errors(None)), [])

    def test_range_reports_record_blob(self):
        self.commit('docs: fixture\n\nProject-Metadata: ' + REF,
                    'reports/actual run.txt')
        errors = in_repo(self.repo, lambda: changed_tree_errors(self.base, 'HEAD'))
        self.assertTrue(any('reports/actual run.txt' in e for e in errors))

    def test_directory_walk_finds_record(self):
        (self.repo / 'reports').mkdir()
        (self.repo / 'reports' / 'actual run.txt').write_bytes(b'captured result')
        errors = directory_errors(self.repo)
        self.assertTrue(any('actual run.txt' in e for e in errors))


if __name__ == '__main__':
    unittest.main()
