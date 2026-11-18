"""CLI regression controls using real gosec against isolated committed Go fixtures."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

GATE = Path(__file__).resolve().parents[1] / 'security_gate.py'
CLEAN = 'package fixture\nfunc Value() string { return "clean" }\n'
BAD = '''package fixture
import "crypto/md5"
func Digest() [16]byte {
 return md5.Sum([]byte("value"))
}
'''


class SecurityGateCLI(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.repo = self.directory / 'repo'
        self.repo.mkdir()
        self.scanner = shutil.which(os.environ.get('GOSEC', 'gosec'))
        if not self.scanner:
            self.fail('Install pinned gosec v2.30.0 or set GOSEC to its executable')
        self.git('init', '-q')
        self.git('config', 'user.name', 'Synthetic security fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')
        (self.repo / 'go.mod').write_text('module example.com/fixture\n\ngo 1.26.0\n')
        (self.repo / 'fixture.go').write_text(CLEAN)
        self.commit()
        self.base = self.git('rev-parse', 'HEAD').strip()

    def git(self, *args):
        return subprocess.check_output(['git', *args], cwd=self.repo,
                                       stderr=subprocess.STDOUT).decode()

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'synthetic fixture')

    def gate(self, base=None, scanner=None, output=None):
        return subprocess.run(['python3', '-B', str(GATE), '--repo', str(self.repo),
                               '--base', base or self.base, '--scanner', scanner or self.scanner,
                               '--output', str(output or self.directory / 'reports')],
                              capture_output=True, text=True)

    def test_clean_candidate_passes(self):
        self.assertEqual(self.gate().returncode, 0)

    def test_export_ignore_cannot_hide_tracked_weak_crypto(self):
        (self.repo / 'weak.go').write_text(BAD)
        (self.repo / '.gitattributes').write_text('weak.go export-ignore\n')
        self.commit()
        self.assertEqual(self.gate().returncode, 1)

    def test_tracked_symlink_fails_closed(self):
        (self.repo / 'linked.go').symlink_to('fixture.go')
        self.commit()
        self.assertEqual(self.gate().returncode, 2)

    def test_tracked_gitlink_fails_closed(self):
        self.git('update-index', '--add', '--cacheinfo', '160000,' + self.base + ',nested')
        self.git('commit', '-qm', 'synthetic gitlink fixture')
        self.assertEqual(self.gate().returncode, 2)

    def test_output_inside_repository_is_rejected_before_writing(self):
        output = self.repo / 'reports'
        result = self.gate(output=output)
        self.assertEqual((result.returncode, output.exists()), (2, False))

    def test_new_nosec_cannot_mask_weak_crypto(self):
        waived = BAD.replace('import "crypto/md5"',
                             'import "crypto/md5" // #nosec G501 -- synthetic attempted waiver')
        waived = waived.replace('return md5.Sum([]byte("value"))',
                                'return md5.Sum([]byte("value")) // #nosec G401 -- synthetic attempted waiver')
        (self.repo / 'fixture.go').write_text(waived)
        self.commit()
        setup = subprocess.run([self.scanner, '-fmt', 'json', '-out',
                                str(self.directory / 'suppressed.json'), './...'],
                               cwd=self.repo, capture_output=True, text=True)
        self.assertEqual(setup.returncode, 0, setup.stderr)
        self.assertEqual(self.gate().returncode, 1)

    def test_real_added_weak_crypto_fails(self):
        (self.repo / 'fixture.go').write_text(BAD)
        self.commit()
        self.assertEqual(self.gate().returncode, 1)

    def test_working_copy_cannot_hide_committed_weak_crypto(self):
        (self.repo / 'fixture.go').write_text(BAD)
        self.commit()
        (self.repo / 'fixture.go').write_text(CLEAN)
        self.assertEqual(self.gate().returncode, 1)

    def test_inherited_findings_survive_line_shift(self):
        (self.repo / 'fixture.go').write_text(BAD)
        self.commit()
        base = self.git('rev-parse', 'HEAD').strip()
        (self.repo / 'fixture.go').write_text('// shifted\n\n' + BAD)
        self.commit()
        result = self.gate(base=base)
        self.assertIn('Inherited findings (unresolved debt, not a security clearance): 2', result.stdout)
        self.assertEqual(result.returncode, 0)

    def test_duplicate_vulnerable_code_fails(self):
        (self.repo / 'fixture.go').write_text(BAD)
        self.commit()
        base = self.git('rev-parse', 'HEAD').strip()
        (self.repo / 'fixture.go').write_text(BAD + 'func DigestAgain() [16]byte {\n return md5.Sum([]byte("value"))\n}\n')
        self.commit()
        self.assertEqual(self.gate(base=base).returncode, 1)

    def test_removed_findings_pass(self):
        (self.repo / 'fixture.go').write_text(BAD)
        self.commit()
        base = self.git('rev-parse', 'HEAD').strip()
        (self.repo / 'fixture.go').write_text(CLEAN)
        self.commit()
        self.assertEqual(self.gate(base=base).returncode, 0)

    def test_missing_scanner_fails_closed(self):
        self.assertEqual(self.gate(scanner=str(self.directory / 'missing')).returncode, 2)

    def test_import_failure_fails_closed(self):
        (self.repo / 'fixture.go').write_text('package fixture\nimport _ "example.invalid/missing"\n')
        self.commit()
        self.assertEqual(self.gate().returncode, 2)

    def test_missing_base_fails_closed(self):
        self.assertEqual(self.gate(base='f' * 40).returncode, 2)

    def test_nonancestor_base_fails_closed(self):
        self.git('checkout', '--orphan', 'unrelated')
        (self.repo / 'fixture.go').write_text(CLEAN + '// unrelated root\n')
        self.commit()
        self.assertEqual(self.gate().returncode, 2)

    def test_malformed_base_fails_closed(self):
        self.assertEqual(self.gate(base='HEAD').returncode, 2)

    def fake_scanner(self, mode):
        path = self.directory / 'scanner'
        # Synthetic report mutations exercise gate failures beyond native findings.
        path.write_text('#!/usr/bin/env python3\n' + '''import json, pathlib, sys
fmt = sys.argv[sys.argv.index('-fmt') + 1]
out = pathlib.Path(sys.argv[sys.argv.index('-out') + 1])
mode = ''' + repr(mode) + '''
if mode == 'exit':
    raise SystemExit(7)
if mode == 'malformed':
    out.write_text('{')
    raise SystemExit(1)
issue = {'file':str(pathlib.Path.cwd()/'fixture.go'), 'rule_id':'G999',
         'details':'synthetic finding', 'code':'1: package fixture', 'line':'1',
         'confidence':('HIGH' if pathlib.Path.cwd().name == 'candidate' else 'LOW') if mode == 'confidence' else 'HIGH',
         'severity':('HIGH' if pathlib.Path.cwd().name == 'candidate' else 'LOW') if mode == 'severity' else 'HIGH'}
if fmt == 'json':
    out.write_text(json.dumps({'Golang errors':{}, 'Issues':[issue], 'Stats':{'files':1,'found':1}}))
else:
    out.write_text(json.dumps({'runs':[{'tool':{},'results':[{}]}]}))
raise SystemExit(1)
''')
        path.chmod(0o755)
        return str(path)

    def test_scanner_process_failure_fails_closed(self):
        self.assertEqual(self.gate(scanner=self.fake_scanner('exit')).returncode, 2)

    def test_malformed_scanner_report_fails_closed(self):
        self.assertEqual(self.gate(scanner=self.fake_scanner('malformed')).returncode, 2)

    def test_changed_confidence_is_a_new_finding(self):
        self.assertEqual(self.gate(scanner=self.fake_scanner('confidence')).returncode, 1)

    def test_changed_severity_is_a_new_finding(self):
        self.assertEqual(self.gate(scanner=self.fake_scanner('severity')).returncode, 1)


if __name__ == '__main__':
    unittest.main()
