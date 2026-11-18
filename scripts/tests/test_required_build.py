"""Execute the deployed Build prerequisite script against failure-state controls."""
import json
import os
import subprocess
import unittest

from scripts.required_build import ROOT, gates

class RequiredBuild(unittest.TestCase):
    def execute(self, script, results):
        return subprocess.run(['bash', '--noprofile', '--norc', '-eo', 'pipefail', '-c', script],
                              env=dict(os.environ, NEEDS_JSON=json.dumps(results)),
                              capture_output=True, text=True)

    def test_all_prerequisites_must_succeed(self):
        for name, jobs, script in gates():
            with self.subTest(workflow=name):
                results = {job: {'result': 'success'} for job in jobs}
                self.assertEqual(self.execute(script, results).returncode, 0)

    def test_each_unsuccessful_state_blocks_build(self):
        for name, jobs, script in gates():
            for job in jobs:
                for state in ('failure', 'skipped', 'cancelled', 'neutral', ''):
                    with self.subTest(workflow=name, job=job, state=state):
                        results = {key: {'result': 'success'} for key in jobs}
                        results[job]['result'] = state
                        self.assertNotEqual(self.execute(script, results).returncode, 0)

    def test_next_build_includes_commit_and_pr_policy(self):
        name, jobs, _ = next(x for x in gates() if x[0] == 'ci-next.yml')
        self.assertIn('commitlint', jobs)
        self.assertIn('pr-policy', jobs)

    def test_next_pr_policy_cannot_be_skipped_as_a_job(self):
        source = (ROOT / '.github/workflows/ci-next.yml').read_text()
        job = source.split('  pr-policy:' + chr(10), 1)[1].split(chr(10) + '  lint:', 1)[0]
        self.assertNotIn('    if: github.event_name', job.split('    steps:', 1)[0])
        # The title must come from the live API (reruns see renames), never
        # the frozen event payload, and must not be interpolated into bash.
        self.assertIn('PR_NUMBER: ${{ github.event.pull_request.number }}', job)
        self.assertNotIn('PR_TITLE: ${{ github.event.pull_request.title }}', job)
        self.assertNotIn('github.event.pull_request.title', job.split('    steps:', 1)[1].split('env:', 1)[1].split('run:', 1)[0])
        self.assertIn('BRANCH: ${{ github.event.pull_request.head.ref }}', job)
        self.assertNotIn('run: echo "${{ github.event.pull_request.title }}"', job)

    def test_commitlint_push_uses_reachable_first_parent(self):
        content = (ROOT / '.github/workflows/ci-next.yml').read_text()
        block = content.split('  commitlint:\n', 1)[1].split('\n  pr-policy:', 1)[0]
        self.assertIn('BASE_SHA="$(git rev-parse HEAD^)"', block)
        self.assertNotIn('github.event.before', block)

    def test_empty_or_malformed_results_fail(self):
        for name, _, script in gates():
            for results in ({}, {'lint': {}}, {'lint': None}, []):
                with self.subTest(workflow=name, results=results):
                    self.assertNotEqual(self.execute(script, results).returncode, 0)


if __name__ == '__main__':
    unittest.main()
