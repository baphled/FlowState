import itertools
import os
from pathlib import Path
import re
import subprocess
import unittest


ROOT = Path(__file__).resolve().parents[2]


class CIQualityTests(unittest.TestCase):
    def test_required_build_actually_fails_on_every_unsuccessful_prerequisite(self):
        for name in ('ci-next.yml', 'ci.yml'):
            text = (ROOT / '.github/workflows' / name).read_text()
            build = text.split('  build:\n', 1)[1].split('  security:\n', 1)[0]
            self.assertIn('    if: always()\n', build)
            self.assertIn('    needs: [lint, test, security]\n', build)
            self.assertNotIn('continue-on-error:', build)
            for key, job in (('LINT', 'lint'), ('TEST', 'test'), ('SECURITY', 'security')):
                self.assertIn(key + '_RESULT: ${{ needs.' + job + '.result }}', build)
            match = re.search(r'        run: \|\n((?:          [^\n]*\n)+)', build)
            self.assertIsNotNone(match)
            script = '\n'.join(line[10:] for line in match.group(1).splitlines())
            for states in itertools.product(('success', 'failure', 'skipped', 'cancelled'), repeat=3):
                with self.subTest(workflow=name, states=states):
                    env = dict(os.environ, LINT_RESULT=states[0], TEST_RESULT=states[1], SECURITY_RESULT=states[2])
                    result = subprocess.run(['bash', '-e', '-c', script], env=env, capture_output=True)
                    self.assertEqual(result.returncode == 0, states == ('success',) * 3)

    def test_security_exit_is_not_masked_and_scanner_is_pinned(self):
        for name in ('ci-next.yml', 'ci.yml'):
            text = (ROOT / '.github/workflows' / name).read_text()
            scan = text.split('      - name: Run gosec\n', 1)[1].split('      - name:', 1)[0]
            self.assertIn('run: gosec -fmt sarif -out gosec-results.sarif ./...', scan)
            self.assertNotRegex(scan, r'\|\||continue-on-error|exit 0')
            self.assertRegex(text, r'go install github.com/securego/gosec/v2/cmd/gosec@v\d+\.\d+\.\d+')
            self.assertNotIn('GO_VERSION:', text)
            self.assertIn('go-version-file: go.mod', text)

    def test_changed_code_lint_uses_actual_base(self):
        for name in ('ci-next.yml', 'ci.yml'):
            text = (ROOT / '.github/workflows' / name).read_text()
            self.assertIn('--config .golangci-new.yml --new-from-rev=${{ github.event.pull_request.base.sha }}', text)


if __name__ == '__main__':
    unittest.main()
