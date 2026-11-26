"""Preflight rejects real admission failure classes without publishing a status."""
import contextlib
import hashlib
import io
import json
import os
import re
import select
import shutil
import signal
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

# The tool-pin classes below need only the standard library, so the local preflight runs
# them with a bare python3. The admission fixtures need `cryptography`; where it is
# missing (a developer machine, never CI) the classes that use them are skipped.
try:
    from scripts import admission_preflight
    from scripts.admission_metadata import AdmissionError
    from scripts.admission_preflight import main, ready_preflight
    from scripts.tests.test_admission_io import (
        HEAD, PARENT, REF, REVIEW_PRIVATE, EXECUTION_PRIVATE, FakeDropbox, FakeGitHub, public, pr, record,
    )
    ADMISSION_FIXTURES = True
except ModuleNotFoundError as error:
    if error.name != 'cryptography' or os.environ.get('CI'):
        raise
    ADMISSION_FIXTURES = False
needs_admission = unittest.skipUnless(ADMISSION_FIXTURES, 'the admission fixtures need cryptography')


@needs_admission
class Readiness(unittest.TestCase):
    def ready(self, github=None, records=None, clock=lambda: 150):
        return ready_preflight(225, PARENT, HEAD, github or FakeGitHub(), records or FakeDropbox(),
                               public(REVIEW_PRIVATE), public(EXECUTION_PRIVATE), clock)

    def test_valid_signed_review_and_green_build_pass_without_publisher(self):
        github = FakeGitHub()
        self.ready(github)
        self.assertEqual(github.statuses, [])

    def test_missing_review_reference_and_wrong_local_base_fail(self):
        for changes in [{'body': ''}, {'base': {'ref': 'next', 'sha': 'f' * 40}}]:
            github = FakeGitHub()
            github.pr = lambda _: pr(**changes)
            with self.subTest(changes=changes):
                with self.assertRaises(AdmissionError):
                    self.ready(github)

    def test_missing_failed_and_skipped_build_fail(self):
        for verdict in ('missing', 'failure', 'skipped'):
            github = FakeGitHub()
            github.checks = [] if verdict == 'missing' else [dict(github.checks[0], conclusion=verdict)]
            with self.subTest(verdict=verdict):
                with self.assertRaisesRegex(AdmissionError, 'required check Build'):
                    self.ready(github)

    def test_changed_head_and_expiring_review_fail(self):
        with self.assertRaisesRegex(AdmissionError, 'state changed'):
            self.ready(FakeGitHub(change_head=True))
        times = iter([150, 200])
        with self.assertRaisesRegex(AdmissionError, 'stale'):
            self.ready(clock=lambda: next(times))

    def test_missing_credentials_and_ready_cache_fail_closed(self):
        args = ['--base', PARENT, '--head', HEAD]
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(main(args), 1)
            self.assertEqual(main(args + ['--phase', 'ready', '--pr', '225', '--records', '/cache']), 1)

    def test_unsigned_execution_fails(self):
        records = FakeDropbox()
        original = records.record
        def unsigned(reference):
            result = original(reference)
            result.pop('attestation', None)
            return result
        records.record = unsigned
        with self.assertRaisesRegex(AdmissionError, 'signature'):
            self.ready(records=records)


GOVERNANCE = ['FlowState/Dev/00-AGENT-READ-FIRST.md', 'FlowState/Dev/AGENTS.md',
              'FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md']
PACK = 'c' * 64


def active(**extra):
    return json.dumps(dict({'docs_pack_manifest_sha256': PACK}, **extra)).encode()


class SyncTree:
    """A temporary owner-private Dropbox tree, its private configuration and a live record."""
    def __init__(self, directory):
        self.root = Path(directory) / 'dropbox'
        self.config = Path(directory) / 'admission.json'
        for name in GOVERNANCE:
            self.write(name, name.encode())
        self.write('FlowState/Metadata/ACTIVE.json', active())
        self.root.chmod(0o700)
        self.configure({'dropbox_local_path': str(self.root)})

    def write(self, name, data):
        (self.root / name).parent.mkdir(parents=True, exist_ok=True)
        (self.root / name).write_bytes(data)

    def configure(self, value):
        self.config.write_text(json.dumps(value))
        self.config.chmod(0o600)

    def live_record(self, **changes):
        reads = [{'path': '/' + name, 'file_id': 'id', 'sha256': hashlib.sha256(name.encode()).hexdigest()}
                 for name in GOVERNANCE]
        reads.append({'path': '/FlowState/Metadata/ACTIVE.json', 'file_id': 'id', 'sha256': 'f' * 64})
        return record(docs_pack_manifest_sha256=PACK, dropbox_reads=reads, **changes)


@needs_admission
class LiveEvidence(unittest.TestCase):
    """The commits phase compares read evidence with live bytes from the configured root."""
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.dir = Path(temp.name)
        self.tree = SyncTree(self.dir)
        repo = self.dir / 'repo'
        repo.mkdir()
        self.addCleanup(os.chdir, os.getcwd())
        os.chdir(repo)
        for args in (['init', '-q'], ['config', 'user.name', 'Synthetic'], ['config', 'user.email', 'f@e.invalid'],
                     ['config', 'commit.gpgsign', 'false'], ['commit', '--allow-empty', '-qm', 'chore: base']):
            self.git(*args)
        self.base = self.git('rev-parse', 'HEAD')
        Path('demo').write_bytes(b'demo')
        self.git('add', 'demo')
        self.git('commit', '-qm', 'chore: candidate\n\nProject-Metadata: ' + REF)
        self.head = self.git('rev-parse', 'HEAD')

    def git(self, *args):
        return subprocess.check_output(['git', *args], stderr=subprocess.PIPE).decode().strip()

    def run_preflight(self, evidence, config=None):
        evidence['base_sha'] = self.base
        evidence['local_candidate'] = {'paths': {'demo': {'sha256': hashlib.sha256(b'demo').hexdigest(), 'bytes': 4}}}
        cache = self.dir / 'records.json'
        cache.write_text(json.dumps({REF: evidence}))
        out, err = io.StringIO(), io.StringIO()
        args = ['--base', self.base, '--head', self.head, '--records', str(cache),
                '--config', str(config or self.tree.config)]
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            return main(args), out.getvalue(), err.getvalue()

    def test_a_valid_record_still_passes_end_to_end(self):
        code, out, _ = self.run_preflight(self.tree.live_record())
        self.assertEqual((code, 'Commit evidence preflight passed' in out), (0, True))

    def test_a_stale_governance_hash_is_refused_naming_the_path_and_the_remedy(self):
        stale = self.tree.live_record()
        stale['dropbox_reads'][2]['sha256'] = 'b' * 64
        code, _, err = self.run_preflight(stale)
        self.assertEqual(code, 1)
        self.assertIn(GOVERNANCE[2], err)
        self.assertIn('rebuild the external record', err)

    def test_a_pack_digest_that_differs_from_active_is_refused(self):
        self.tree.write('FlowState/Metadata/ACTIVE.json', active(docs_pack_manifest_sha256='d' * 64))
        code, _, err = self.run_preflight(self.tree.live_record())
        self.assertEqual((code, 'docs_pack_manifest_sha256' in err), (1, True))

    def test_a_changed_active_json_is_still_accepted(self):
        self.tree.write('FlowState/Metadata/ACTIVE.json', active(moving='pointer'))
        self.assertEqual(self.run_preflight(self.tree.live_record())[0], 0)

    def test_without_a_root_the_check_fails_closed_naming_the_setting(self):
        self.tree.configure({'repository': 'baphled/FlowState'})
        missing = self.dir / 'absent.json'
        for config in (self.tree.config, missing):
            with self.subTest(config=config.name):
                code, _, err = self.run_preflight(self.tree.live_record(), config)
                self.assertEqual((code, 'dropbox_local_path' in err), (1, True))

    def test_a_group_readable_configuration_is_refused(self):
        self.tree.config.chmod(0o644)
        self.assertEqual(self.run_preflight(self.tree.live_record())[0], 1)

    def test_a_rebuilt_record_at_the_same_path_passes_under_the_existing_review(self):
        stale = self.tree.live_record()
        stale['dropbox_reads'][0]['sha256'] = 'b' * 64
        with self.assertRaisesRegex(AdmissionError, 'rebuild the external record'):
            admission_preflight.verify_live(stale, str(self.tree.root))
        rebuilt = self.tree.live_record()
        self.assertIsNone(admission_preflight.verify_live(rebuilt, str(self.tree.root)))
        for stored in (stale, rebuilt):
            records = FakeDropbox()
            original = records.record
            records.record = lambda reference, stored=stored: stored if reference == REF else original(reference)
            admission_preflight.ready_preflight(225, PARENT, HEAD, FakeGitHub(), records,
                                                public(REVIEW_PRIVATE), public(EXECUTION_PRIVATE), lambda: 150)


@needs_admission
class PushHook(unittest.TestCase):
    def test_checks_each_pushed_tip_and_propagates_failure(self):
        hook = Path(__file__).resolve().parents[2] / '.githooks/pre-push'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            programs = {'git': '#!/bin/sh\ncase "$1" in rev-parse) echo ' + PARENT + ';; esac\n',
                        'python3': '#!/bin/sh\nexit 0\n',
                        'make': '#!/bin/sh\necho "$*" >> "$LOG"\nexit "${FAILURE:-0}"\n'}
            for name, source in programs.items():
                path = root / name
                path.write_text(source)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + ':' + os.environ['PATH'], LOG=str(root / 'log'))
            tips = f'refs/heads/other {HEAD} refs/heads/other {PARENT}\n'
            tips += f'refs/heads/second {PARENT} refs/heads/second {HEAD}\n'
            result = subprocess.run(['/bin/sh', str(hook)], input=tips, text=True, env=env)
            self.assertEqual(result.returncode, 0)
            calls = (root / 'log').read_text()
            self.assertIn('HEAD_SHA=' + HEAD, calls)
            self.assertIn('HEAD_SHA=' + PARENT, calls)
            result = subprocess.run(['/bin/sh', str(hook)], input=tips, text=True,
                                    env=dict(env, FAILURE='7'))
            self.assertEqual(result.returncode, 7)

    def test_warns_for_every_pushed_tip_that_is_not_head_but_checks_only_branches(self):
        hook = Path(__file__).resolve().parents[2] / '.githooks/pre-push'
        zero = '0' * 40
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            programs = {'git': '#!/bin/sh\ncase "$1" in rev-parse) echo ' + PARENT + ';; esac\n',
                        'python3': '#!/bin/sh\nexit 0\n',
                        'make': '#!/bin/sh\necho "$*" >> "$LOG"\n'}
            for name, source in programs.items():
                path = root / name
                path.write_text(source)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + ':' + os.environ['PATH'], LOG=str(root / 'log'))
            tips = (f'HEAD~1 {HEAD} refs/heads/raw {zero}\n'
                    f'{HEAD} {HEAD} refs/heads/sha {zero}\n'
                    f'refs/tags/v1 {HEAD} refs/tags/v1 {zero}\n'
                    f'(delete) {zero} refs/heads/gone {HEAD}\n'
                    f'refs/heads/head {PARENT} refs/heads/head {zero}\n'
                    f'refs/heads/other {HEAD} refs/tags/viaother {zero}\n')
            result = subprocess.run(['/bin/sh', str(hook)], input=tips, text=True, env=env,
                                    capture_output=True)
            self.assertEqual(result.returncode, 0)
            warnings = [line for line in result.stderr.splitlines() if 'warning' in line]
            self.assertEqual(len(warnings), 2)
            self.assertIn('pre-push: warning: HEAD~1 is not the checked-out HEAD; '
                          'the local CI preflight does not cover it', warnings)
            # Both branch tips are admission-checked, including the one pushed to a tag name.
            self.assertEqual((root / 'log').read_text().count('check-admission-commits'), 2)

    def test_non_head_tip_warns_without_a_preflight_and_head_tip_does_not_warn(self):
        hook = Path(__file__).resolve().parents[2] / '.githooks/pre-push'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            programs = {'git': '#!/bin/sh\ncase "$1" in rev-parse) echo ' + PARENT + ';; esac\n',
                        'python3': '#!/bin/sh\nexit 0\n',
                        'make': '#!/bin/sh\necho "$*" >> "$LOG"\n'}
            for name, source in programs.items():
                (root / name).write_text(source)
                (root / name).chmod(0o755)
            env = dict(os.environ, PATH=str(root) + ':' + os.environ['PATH'], LOG=str(root / 'log'))
            run = lambda tip: subprocess.run(['/bin/sh', str(hook)], input=tip, text=True, env=env,
                                             capture_output=True)
            other = run(f'refs/heads/other {HEAD} refs/heads/other {PARENT}\n')
            self.assertIn('refs/heads/other is not the checked-out HEAD', other.stderr)
            calls = (root / 'log').read_text().splitlines()
            self.assertEqual([c for c in calls if c.startswith('preflight')], ['preflight BASE_SHA=' + PARENT])
            self.assertNotIn(HEAD, [c for c in calls if c.startswith('preflight')][0])
            self.assertNotIn('warning', run(f'refs/heads/me {PARENT} refs/heads/me {HEAD}\n').stderr)

    def test_failing_ci_preflight_aborts_the_push(self):
        hook = Path(__file__).resolve().parents[2] / '.githooks/pre-push'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            programs = {'git': '#!/bin/sh\ncase "$1" in rev-parse) echo ' + PARENT + ';; esac\n',
                        'python3': '#!/bin/sh\nexit 0\n',
                        'make': '#!/bin/sh\necho "$*" >> "$LOG"\n'
                                'case "$1" in preflight) exit 9;; esac\nexit 0\n'}
            for name, source in programs.items():
                path = root / name
                path.write_text(source)
                path.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + ':' + os.environ['PATH'], LOG=str(root / 'log'))
            tips = f'refs/heads/other {PARENT} refs/heads/other {HEAD}\n'
            result = subprocess.run(['/bin/sh', str(hook)], input=tips, text=True, env=env)
            self.assertEqual(result.returncode, 9)
            self.assertIn('preflight BASE_SHA=' + PARENT, (root / 'log').read_text())


class ToolPins(unittest.TestCase):
    """Makefile `make preflight` tool pins must match the versions CI installs."""
    root = Path(__file__).resolve().parents[2]

    @staticmethod
    def logical_lines(lines):
        """Yield (first line number, text), joining lines continued with a trailing backslash."""
        number, text = 0, ''
        for index, line in enumerate(lines, 1):
            if not text:
                number = index
            # A comment or a step name is never continued by a trailing backslash, so it cannot
            # swallow the install line that follows it.
            if re.search(r'\\\s*$', line) and not re.match(r'\s*(#|-?\s*name:)', text + line):
                text += re.sub(r'\\\s*$', '', line).rstrip() + ' '
                continue
            yield number, text + line.lstrip() if text else line
            text = ''
        if text:
            yield number, text.rstrip()

    def pins(self, tool):
        """Return (location, version) for every pin of the tool in any workflow."""
        found = []
        for path in sorted((self.root / '.github/workflows').glob('*.yml')):
            lines = path.read_text().splitlines()
            env = {m.group(1): m.group(2) for m in
                   (re.match(r'\s*(\w+_VERSION): "?(v[\d.]+)"?\s*$', line) for line in lines) if m}
            for number, line in self.logical_lines(lines):
                where = f'{path.relative_to(self.root)}:{number}'
                # A comment or a step name that mentions installing the tool is not an install.
                command = not re.match(r'\s*(#|-?\s*name:)', line)
                installs = command and re.search(r'\b(go install|curl|wget|brew install)\b', line) and tool in line
                if tool == 'gosec':
                    m = re.search(r'gosec/v2/cmd/gosec@(\$\{\{ \w+\.\w+ \}\}|\S+)', line)
                    if m:
                        ref = re.match(r'\$\{\{ env\.(\w+) \}\}', m.group(1))
                        found.append((where, env.get(ref.group(1), m.group(1)) if ref else m.group(1)))
                    elif installs:
                        found.append((where, 'unrecognised install'))
                elif 'golangci-lint-action' in line:
                    window = ' '.join(lines[number:number + 6])
                    m = re.search(r'version: (\S+)', window)
                    found.append((where, m.group(1) if m else 'unpinned'))
                elif re.search(r'golangci-lint/HEAD/install\.sh', line):
                    found.append((where, line.split()[-1]))
                elif installs:
                    found.append((where, 'unrecognised install'))
        return found

    def test_every_workflow_takes_its_go_version_from_go_mod(self):
        """A workflow that names its own Go version drifts from go.mod (release.yml did)."""
        offenders = []
        for workflow in sorted((self.root / '.github/workflows').glob('*.yml')):
            for number, line in enumerate(workflow.read_text().splitlines(), 1):
                if re.match(r'\s*go-version:', line) or re.match(r'\s*GO_VERSION:', line):
                    offenders.append(f'{workflow.name}:{number}')
        self.assertEqual(offenders, [], 'use `go-version-file: go.mod`: ' + ', '.join(offenders))

    def test_makefile_pins_match_every_workflow_pin(self):
        makefile = (self.root / 'Makefile').read_text()
        for tool, variable in (('golangci-lint', 'GOLANGCI_LINT_VERSION'), ('gosec', 'GOSEC_VERSION')):
            match = re.search(rf'^{variable} := (\S+)', makefile, re.M)
            self.assertIsNotNone(match, tool)
            pins = self.pins(tool)
            self.assertTrue(pins, tool + ' is not installed by any workflow')
            for where, version in pins:
                with self.subTest(tool=tool, where=where):
                    self.assertEqual(version, match.group(1),
                                     f'{tool}: Makefile:{variable} pins {match.group(1)} '
                                     f'but {where} pins {version}')


class UnrecognisedToolInstall(unittest.TestCase):
    """An install line in a style ToolPins does not know is reported, never silently skipped."""
    def pins_of(self, tool, line):
        with tempfile.TemporaryDirectory() as root:
            workflows = Path(root) / '.github/workflows'
            workflows.mkdir(parents=True)
            (workflows / 'ci.yml').write_text(f'jobs:\n  lint:\n    steps:\n      - run: {line}\n')
            reader = ToolPins()
            reader.root = Path(root)
            return [version for _, version in reader.pins(tool)]

    def test_another_golangci_lint_install_style_is_reported(self):
        line = 'go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v9.9.9'
        self.assertEqual(self.pins_of('golangci-lint', line), ['unrecognised install'])

    def test_another_gosec_install_style_is_reported(self):
        line = 'curl -sfL https://example.invalid/gosec/install.sh | sh -s v9.9.9'
        self.assertEqual(self.pins_of('gosec', line), ['unrecognised install'])

    def test_a_known_gosec_install_is_not_reported_twice(self):
        line = 'go install github.com/securego/gosec/v2/cmd/gosec@v2.30.0'
        self.assertEqual(self.pins_of('gosec', line), ['v2.30.0'])

    def test_an_install_continued_over_lines_is_still_read(self):
        with tempfile.TemporaryDirectory() as root:
            workflows = Path(root) / '.github/workflows'
            workflows.mkdir(parents=True)
            (workflows / 'ci.yml').write_text(
                'jobs:\n  lint:\n    steps:\n      - run: |\n'
                '          go install \\\n            github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v9.9.9\n')
            reader = ToolPins()
            reader.root = Path(root)
            self.assertEqual([version for _, version in reader.pins('golangci-lint')], ['unrecognised install'])

    def test_a_comment_ending_in_a_backslash_does_not_hide_the_install_after_it(self):
        for lead in ('          # tidy up \\', '      - name: tidy up \\'):
            with self.subTest(lead=lead), tempfile.TemporaryDirectory() as root:
                workflows = Path(root) / '.github/workflows'
                workflows.mkdir(parents=True)
                (workflows / 'ci.yml').write_text(
                    'jobs:\n  lint:\n    steps:\n' + lead + '\n'
                    '      - run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v9.9.9\n')
                reader = ToolPins()
                reader.root = Path(root)
                self.assertEqual([v for _, v in reader.pins('golangci-lint')], ['unrecognised install'])

    def test_a_continued_gosec_install_reports_its_version(self):
        with tempfile.TemporaryDirectory() as root:
            workflows = Path(root) / '.github/workflows'
            workflows.mkdir(parents=True)
            (workflows / 'ci.yml').write_text(
                'jobs:\n  lint:\n    steps:\n      - run: |\n'
                '          go install \\\n            github.com/securego/gosec/v2/cmd/gosec@v9.9.9\n')
            reader = ToolPins()
            reader.root = Path(root)
            self.assertEqual([version for _, version in reader.pins('gosec')], ['v9.9.9'])

    def test_a_line_that_only_runs_the_tool_is_not_an_install(self):
        self.assertEqual(self.pins_of('golangci-lint', 'golangci-lint run ./...'), [])

    def test_a_comment_or_step_name_about_installing_is_not_an_install(self):
        for text in ('# we used to curl gosec', '- name: Install gosec with go install'):
            with self.subTest(text), tempfile.TemporaryDirectory() as root:
                workflows = Path(root) / '.github/workflows'
                workflows.mkdir(parents=True)
                (workflows / 'ci.yml').write_text(f'jobs:\n  lint:\n    steps:\n      {text}\n')
                reader = ToolPins()
                reader.root = Path(root)
                self.assertEqual(reader.pins('gosec'), [])


class ToolInstall(unittest.TestCase):
    """The install rule and the node_modules stamp, run against fakes in a path with a space."""
    root = Path(__file__).resolve().parents[2]

    def scratch(self, directory, files, fakes):
        work = Path(directory) / 'a repo'
        (work / 'scripts').mkdir(parents=True)
        for name in files:
            shutil.copy(self.root / name, work / name)
        bins = Path(directory) / 'fake bin'
        bins.mkdir()
        for name, source in fakes.items():
            (bins / name).write_text('#!/bin/sh\n' + source)
            (bins / name).chmod(0o755)
        env = dict(os.environ, PATH=str(bins) + ':' + os.environ['PATH'])
        return work, env

    def test_makefile_rule_installs_atomically_in_a_path_with_a_space(self):
        fake_go = 'echo "$GOTOOLCHAIN $GOBIN $*" >> "$LOG"\nname=${2##*/}\nprintf built > "$GOBIN/${name%@*}"\n'
        with tempfile.TemporaryDirectory() as directory:
            work, env = self.scratch(directory, ['Makefile', 'go.mod', 'scripts/install_go_tool.sh'],
                                     {'go': fake_go})
            env['LOG'] = str(Path(directory) / 'log')
            target = 'bin/tools/gosec-' + re.search(r'^GOSEC_VERSION := (\S+)',
                                                    (work / 'Makefile').read_text(), re.M).group(1)
            done = subprocess.run(['make', '-s', target], cwd=work, env=env, text=True, capture_output=True)
            self.assertEqual(done.returncode, 0, done.stderr)
            self.assertEqual((work / target).read_text(), 'built')
            self.assertEqual(sorted(p.name for p in (work / 'bin/tools').iterdir()), [Path(target).name])
            self.assertIn('go1.', (Path(directory) / 'log').read_text())
            self.assertIn('a repo/bin/tools/.install.', (Path(directory) / 'log').read_text())

    def test_failed_install_leaves_no_file_at_the_versioned_path(self):
        with tempfile.TemporaryDirectory() as directory:
            work, env = self.scratch(directory, ['scripts/install_go_tool.sh'],
                                     {'go': 'printf partial > "$GOBIN/gosec"\nexit 3\n'})
            final = work / 'bin/tools/gosec-v1'
            done = subprocess.run(['bash', 'scripts/install_go_tool.sh', 'x/gosec@v1', 'gosec', str(final)],
                                  cwd=work, env=env, capture_output=True)
            self.assertEqual(done.returncode, 3)
            self.assertEqual(list((work / 'bin/tools').iterdir()), [])

    def install(self, work, env, final):
        return subprocess.run(['bash', 'scripts/install_go_tool.sh', 'x/gosec@v1', 'gosec', str(final)],
                              cwd=work, env=env, capture_output=True)

    def test_successful_install_places_the_binary_and_leaves_no_temporary_file(self):
        with tempfile.TemporaryDirectory() as directory:
            work, env = self.scratch(directory, ['scripts/install_go_tool.sh'],
                                     {'go': 'printf built > "$GOBIN/gosec"\n'})
            final = work / 'bin/tools/gosec-v1'
            self.assertEqual(self.install(work, env, final).returncode, 0)
            self.assertEqual(sorted(p.name for p in final.parent.iterdir()), ['gosec-v1'])
            self.assertEqual(final.read_text(), 'built')

    def test_failed_build_keeps_the_previous_binary_and_leaves_no_temporary_file(self):
        with tempfile.TemporaryDirectory() as directory:
            work, env = self.scratch(directory, ['scripts/install_go_tool.sh'],
                                     {'go': 'printf partial > "$GOBIN/gosec"\nexit 3\n'})
            final = work / 'bin/tools/gosec-v1'
            final.parent.mkdir(parents=True)
            final.write_text('previous')
            self.assertEqual(self.install(work, env, final).returncode, 3)
            self.assertEqual((final.read_text(), sorted(p.name for p in final.parent.iterdir())),
                             ('previous', ['gosec-v1']))

    def test_terminated_install_leaves_no_temporary_file(self):
        stub = 'printf partial > "$GOBIN/gosec"\necho "started $$"\nexec sleep 60\n'
        for signal_number in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
            with self.subTest(signal=signal_number.name), tempfile.TemporaryDirectory() as directory:
                work, env = self.scratch(directory, ['scripts/install_go_tool.sh'], {'go': stub})
                final = work / 'bin/tools/gosec-v1'
                run = subprocess.Popen(['bash', 'scripts/install_go_tool.sh', 'x/gosec@v1', 'gosec', str(final)],
                                       cwd=work, env=env, stdout=subprocess.PIPE)
                try:
                    # A regression must fail, not hang: bound the read and the wait.
                    ready = select.select([run.stdout], [], [], 20)[0]
                    if not ready:
                        raise AssertionError('the stub go never started')
                    child = int(run.stdout.readline().split()[1])
                    run.send_signal(signal_number)
                    status = run.wait(timeout=20)
                finally:
                    if run.poll() is None:
                        run.kill()
                        run.wait()
                    run.stdout.close()
                self.assertNotEqual(status, 0)
                self.assertEqual(list(final.parent.iterdir()), [])
                with self.assertRaises(ProcessLookupError):
                    os.kill(child, 0)

    def ensure(self, work, env):
        return subprocess.run(['sh', 'scripts/ensure_node_modules.sh'], cwd=work, env=env,
                              capture_output=True, text=True)

    def test_node_modules_is_reinstalled_only_when_the_lock_changed(self):
        fake_npm = 'echo "$*" >> "$LOG"\nmkdir -p node_modules\n'
        with tempfile.TemporaryDirectory() as directory:
            work, env = self.scratch(directory, ['scripts/ensure_node_modules.sh'], {'npm': fake_npm})
            env['LOG'] = str(Path(directory) / 'log')
            (work / 'package-lock.json').write_text('one')
            runs = lambda: (Path(directory) / 'log').read_text().count('ci') if (Path(directory) / 'log').exists() else 0
            (work / 'node_modules').mkdir()           # present but never stamped
            self.assertEqual(self.ensure(work, env).returncode, 0)
            self.assertEqual(runs(), 1)
            self.ensure(work, env)                    # unchanged lock
            self.assertEqual(runs(), 1)
            (work / 'package-lock.json').write_text('two')
            self.ensure(work, env)                    # changed lock
            self.assertEqual(runs(), 2)

    def test_failed_npm_ci_leaves_no_stamp(self):
        with tempfile.TemporaryDirectory() as directory:
            work, env = self.scratch(directory, ['scripts/ensure_node_modules.sh'],
                                     {'npm': 'mkdir -p node_modules\nexit 4\n'})
            (work / 'package-lock.json').write_text('one')
            self.assertEqual(self.ensure(work, env).returncode, 4)
            self.assertFalse((work / 'node_modules/.package-lock.sha256').exists())


class WorkflowPins(unittest.TestCase):
    """Workflow text that nothing else pins (kept here: the contract needs an owning module)."""
    workflows = Path(__file__).resolve().parents[2] / '.github/workflows'

    def test_local_preflight_runs_the_tool_pin_tests_without_the_network_or_a_virtualenv(self):
        recipe = (self.workflows.parents[1] / 'Makefile').read_text().split('\npreflight:', 1)[1].split('\n\n', 1)[0]
        pinned = [line for line in recipe.splitlines() if 'unittest' in line and 'ToolPins' in line]
        self.assertEqual([line.split()[0] for line in pinned], ['python3'])

    def test_size_label_bound_is_the_selected_budget_plus_one(self):
        block = (self.workflows / 'pr-validation.yml').read_text().split('  size-label:', 1)[1]
        step = block.split('id: review_budget', 1)[1].split('pr-size-labeler', 1)[0]
        self.assertIn('--policy "$RUNNER_TEMP/env_ci_base" --title "$pr_title"', step)
        self.assertIn('budget="$(python3 -B "$RUNNER_TEMP/pr_review_budget_base.py"', step)
        self.assertIn('echo "limit=$((budget + 1))" >> "$GITHUB_OUTPUT"', step)
        self.assertIn('l_max_size: ${{ steps.review_budget.outputs.limit }}', block)
        self.assertNotRegex(block, r'l_max_size: \d')

    def test_admission_liveness_runs_always_for_non_draft_prs_into_next(self):
        ci = (self.workflows / 'ci-next.yml').read_text()
        head = ci.split('  admission-result:', 1)[1].split('    runs-on:', 1)[0]
        condition = ' '.join(head.split('if: >-', 1)[1].split())
        self.assertIn('always()', condition)
        self.assertIn("github.event_name == 'pull_request'", condition)
        self.assertIn('!github.event.pull_request.draft', condition)
        self.assertIn("github.event.pull_request.base.ref == 'next'", condition)
