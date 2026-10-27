import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / 'worktree_policy.py'


class WorktreeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env = dict(os.environ, GIT_CONFIG_NOSYSTEM='1',
                        GIT_AUTHOR_NAME='fixture', GIT_AUTHOR_EMAIL='fixture@example.invalid',
                        GIT_COMMITTER_NAME='fixture', GIT_COMMITTER_EMAIL='fixture@example.invalid')
        for key in ('GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GIT_COMMON_DIR'):
            self.env.pop(key, None)
        self.seed = self.root / 'seed'
        self.git('init', '-b', 'next', str(self.seed))
        (self.seed / 'source.txt').write_text('fixture')
        self.git('-C', str(self.seed), 'add', '.')
        self.git('-C', str(self.seed), 'commit', '-qm', 'chore: fixture')
        self.remote = self.root / 'remote.git'
        self.git('clone', '--bare', str(self.seed), str(self.remote))
        self.bare = self.root / 'FlowState.git'
        self.git('clone', '--bare', str(self.remote), str(self.bare))
        self.main = self.bare / 'next-wt'
        self.git('--git-dir', str(self.bare), 'worktree', 'add', str(self.main), 'next')

    def git(self, *args):
        return subprocess.run(['git', *args], env=self.env, text=True, capture_output=True, check=True).stdout.strip()

    def run_policy(self, *args, cwd=None):
        return subprocess.run(['python3', '-B', str(SCRIPT), *args], cwd=cwd or self.main,
                              env=self.env, text=True, capture_output=True)

    def create(self):
        result = self.run_policy('new', 'test-feature')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.feature = self.bare / 'test-feature'
        self.head = self.git('-C', str(self.feature), 'rev-parse', 'HEAD')
        self.git('-C', str(self.feature), 'push', 'origin', 'feature/test-feature')

    def approval(self, **changes):
        value = {'metadata_ref':'dropbox:/FlowState/Metadata/attempts/fixture/approval.json',
                 'pr_url':'https://github.com/baphled/FlowState/pull/123', 'state':'approved',
                 'github_evidence_ref':'fixture-only', 'approved_head_sha':self.head,
                 'branch':'feature/test-feature'}
        value.update(changes)
        path = self.root / 'approval.json'
        path.write_text(json.dumps(value))
        return str(path)

    def test_new_is_inside_bare_root_from_actual_next(self):
        self.create()
        self.assertEqual(self.feature.parent, self.bare)
        self.assertEqual(self.head, self.git('--git-dir', str(self.remote), 'rev-parse', 'next'))

    def test_rejects_separate_clone_and_escaping_name(self):
        self.assertNotEqual(self.run_policy('new', 'outside', cwd=self.seed).returncode, 0)
        self.assertNotEqual(self.run_policy('new', '../outside').returncode, 0)

    def test_approved_clean_pushed_worktree_removed_branch_retained(self):
        self.create()
        result = self.run_policy('cleanup', 'test-feature', '--approval-record', self.approval())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.feature.exists())
        self.assertEqual(self.git('--git-dir', str(self.bare), 'rev-parse', 'feature/test-feature'), self.head)

    def test_cleanup_rejects_dirty_and_wrong_head(self):
        self.create()
        result = self.run_policy('cleanup', 'test-feature', '--approval-record', self.approval(approved_head_sha='b'*40))
        self.assertNotEqual(result.returncode, 0)
        (self.feature / 'untracked.txt').write_text('preserve')
        result = self.run_policy('cleanup', 'test-feature', '--approval-record', self.approval())
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.feature.exists())

    def test_cleanup_rejects_unpushed_and_missing_approval(self):
        self.create()
        (self.feature / 'source.txt').write_text('new unpushed work')
        self.git('-C', str(self.feature), 'commit', '-qam', 'chore: unpushed')
        self.head = self.git('-C', str(self.feature), 'rev-parse', 'HEAD')
        self.assertNotEqual(self.run_policy('cleanup', 'test-feature', '--approval-record', self.approval()).returncode, 0)
        self.assertNotEqual(self.run_policy('cleanup', 'test-feature').returncode, 0)

    def test_cleanup_preserves_skip_worktree_hidden_changes(self):
        self.create()
        self.git('-C', str(self.feature), 'update-index', '--skip-worktree', 'source.txt')
        (self.feature / 'source.txt').write_text('preserve hidden change')
        result = self.run_policy('cleanup', 'test-feature', '--approval-record', self.approval())
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.feature / 'source.txt').read_text(), 'preserve hidden change')

    def test_cleanup_preserves_assume_unchanged_hidden_changes(self):
        self.create()
        self.git('-C', str(self.feature), 'update-index', '--assume-unchanged', 'source.txt')
        (self.feature / 'source.txt').write_text('preserve hidden change')
        result = self.run_policy('cleanup', 'test-feature', '--approval-record', self.approval())
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.feature / 'source.txt').read_text(), 'preserve hidden change')

    def test_cleanup_preserves_ignored_local_files(self):
        self.create()
        self.git('-C', str(self.feature), 'config', 'core.excludesFile', str(self.root / 'ignored'))
        (self.root / 'ignored').write_text('.env\n')
        (self.feature / '.env').write_text('preserve local file')
        result = self.run_policy('cleanup', 'test-feature', '--approval-record', self.approval())
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((self.feature / '.env').exists())

    def test_cleanup_accepts_pushed_pr_ref_after_remote_branch_deletion(self):
        self.create()
        self.git('--git-dir', str(self.remote), 'update-ref', 'refs/pull/123/head', self.head)
        self.git('--git-dir', str(self.remote), 'update-ref', '-d', 'refs/heads/feature/test-feature')
        result = self.run_policy('cleanup', 'test-feature', '--approval-record', self.approval(state='merged'))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.feature.exists())

    def test_merged_ancestor_branch_is_deleted(self):
        self.create()
        result = self.run_policy('cleanup', 'test-feature', '--approval-record', self.approval(state='merged'))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(json.loads(result.stdout)['branch_deleted'])


if __name__ == '__main__':
    unittest.main()
