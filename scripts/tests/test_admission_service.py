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
        self.assertIn('Restart=on-failure', unit(Path('/safe'), Path('/config')))
        self.assertIn('Type=notify', unit(Path('/safe'), Path('/config')))
