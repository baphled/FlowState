"""Execute the deployed Build prerequisite script against failure-state controls."""
import json
import os
from pathlib import Path
import re
import subprocess
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[2]


def gates():
    for name in ('ci-next.yml', 'ci.yml'):
        source = (ROOT / '.github/workflows' / name).read_text()
        build = source.split('  build:\n', 1)[1].split('\n  security:', 1)[0]
        if 'name: Require successful prerequisites' not in build:
            if name == 'ci-next.yml':
                raise AssertionError('required next Build has no failure gate')
            continue
        if 'if: ${{ always() }}' not in build:
            raise AssertionError('required Build can skip after prerequisite failure')
        block = build.split('name: Require successful prerequisites', 1)[1]
        script = block.split('run: |\n', 1)[1].split('\n      - ', 1)[0]
        jobs = re.search(r'needs: \[([^]]+)\]', build)[1].replace(' ', '').split(',')
        yield name, jobs, textwrap.dedent(script)


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

    def test_empty_or_malformed_results_fail(self):
        for name, _, script in gates():
            for results in ({}, {'lint': {}}, {'lint': None}, []):
                with self.subTest(workflow=name, results=results):
                    self.assertNotEqual(self.execute(script, results).returncode, 0)


if __name__ == '__main__':
    unittest.main()
