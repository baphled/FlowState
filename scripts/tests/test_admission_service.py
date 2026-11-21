"""Package contents, managed startup and CI monitor wiring contracts."""
import subprocess
import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest.mock import patch

from scripts.admission_metadata import AdmissionError
from scripts.admission_service import install, main, package, unit


class Service(unittest.TestCase):
    def test_package_is_executable_and_contains_committed_sources_only(self):
        def git(*args):
            if args[1] == 'rev-parse': return b'a' * 40
            if args[1] == 'ls-tree':
                return b'scripts/admission_runner.py\nscripts/tests/test_secret.py\n'
            if args[-1].endswith('admission_runner.py'):
                return b'def main():\n    return 7\ndef terminate(*args):\n    pass\n'
            return b'synthetic fixture'
        with tempfile.TemporaryDirectory() as directory:
            artifact = Path(directory) / 'runner.pyz'
            with patch('scripts.admission_service.command', side_effect=git): package(artifact)
            with zipfile.ZipFile(artifact) as archive:
                self.assertNotIn('scripts/tests/test_secret.py', archive.namelist())
                self.assertEqual(archive.read('SOURCE_SHA'), b'a' * 40 + b'\n')
            self.assertEqual(subprocess.run(['python3', str(artifact)]).returncode, 7)

    def test_invalid_config_never_installs_or_starts_a_service(self):
        with patch('scripts.admission_service.private_file', side_effect=AdmissionError('missing config')), \
                patch('scripts.admission_service.command') as command:
            self.assertEqual(main(['start']), 1)
            with self.assertRaises(AdmissionError): install('absent', Path('/unused'), Path('/missing'))
            command.assert_not_called()

    def test_start_checks_package_and_requires_active_service(self):
        with patch('scripts.admission_service.private_file'), \
                patch('scripts.admission_service.command', return_value=b'') as command:
            self.assertEqual(main(['start']), 0)
            self.assertIn('--check-config', command.call_args_list[0].args)
            self.assertEqual(command.call_args_list[-1].args[:4],
                             ('systemctl', '--user', 'is-active', '--quiet'))
        self.assertIn('Restart=always', unit(Path('/safe'), Path('/config')))
        self.assertIn('Type=notify', unit(Path('/safe'), Path('/config')))

    def test_status_also_reports_restart_state_even_when_the_unit_is_not_active(self):
        calls = []

        def systemctl(*args):
            calls.append(args)
            if args[2] == 'status':
                raise subprocess.CalledProcessError(3, args)
            return b'ActiveState=activating\nSubState=auto-restart\nNRestarts=9\nResult=exit-code\n'
        with patch('scripts.admission_service.command', side_effect=systemctl), \
                patch('builtins.print') as printed:
            self.assertEqual(main(['status']), 1)
        show = ('systemctl', '--user', 'show', '-p', 'ActiveState,SubState,NRestarts,Result',
                'flowstate-admission.service')
        self.assertEqual(calls[0], show)
        self.assertIn('NRestarts=9', printed.call_args_list[0].args[0])

    def test_status_runs_show_and_status_when_active(self):
        with patch('scripts.admission_service.command', return_value=b'ok') as command:
            self.assertEqual(main(['status']), 0)
        self.assertEqual([c.args[2] for c in command.call_args_list], ['show', 'status'])

    def test_unit_never_stops_restarting_the_publisher(self):
        text = unit(Path('/safe'), Path('/config'))
        self.assertIn('StartLimitIntervalSec=0', text)
        self.assertNotIn('StartLimitBurst', text)
        self.assertIn('RestartSec=30', text)

    def test_monitor_follows_build_without_creating_a_dependency_cycle(self):
        root = Path(__file__).resolve().parents[2]
        workflow = (root / '.github/workflows/ci-next.yml').read_text()
        monitor = workflow.split('  admission-result:', 1)[1].split('  scope:', 1)[0]
        self.assertIn('needs: [build]', monitor)
        self.assertIn("!github.event.pull_request.draft && github.event.pull_request.base.ref == 'next'", monitor)
        self.assertIn('timeout-minutes: 4', monitor)
        self.assertIn('scripts.admission_runner --watch', monitor)
        self.assertIn('session-start: admission-runner-start', (root / 'Makefile').read_text())
