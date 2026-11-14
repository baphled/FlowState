"""Private startup configuration must fail before any network or service mutation."""
import json
import tempfile
import unittest
from pathlib import Path

from scripts.admission_config import configuration
from scripts.admission_metadata import AdmissionError
from scripts.tests.test_app_credentials import private_key, pem


class Configuration(unittest.TestCase):
    def test_dropbox_token_and_local_path_are_mutually_exclusive(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            key = root / 'key.pem'
            key.write_bytes(pem(private_key()))
            key.chmod(0o600)
            path = root / 'config.json'
            base = {'repository': 'baphled/FlowState', 'repository_id': 1191083392,
                    'app_id': 5728089, 'installation_id': 181235940,
                    'github_read_token': 'synthetic', 'dropbox_read_token': 'synthetic',
                    'private_key_file': str(key), 'review_public_key': 'a' * 64,
                    'execution_public_key': 'b' * 64}
            path.write_text(json.dumps(dict(base, dropbox_local_path=str(root))))
            path.chmod(0o600)
            with self.assertRaisesRegex(AdmissionError, 'exactly one'):
                configuration(path)
            neither = {k: v for k, v in base.items() if k != 'dropbox_read_token'}
            path.write_text(json.dumps(neither))
            with self.assertRaisesRegex(AdmissionError, 'exactly one'):
                configuration(path)
            path.write_text(json.dumps({**neither, 'dropbox_local_path': str(root)}))
            self.assertEqual(configuration(path)['dropbox_local_path'], str(root))

    def test_dropbox_local_path_requires_private_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            key = root / 'key.pem'
            key.write_bytes(pem(private_key()))
            key.chmod(0o600)
            path = root / 'config.json'
            local = root / 'tree'
            local.mkdir()
            (local / 'record.json').write_text('{}')
            value = {'repository': 'baphled/FlowState', 'repository_id': 1191083392,
                     'app_id': 5728089, 'installation_id': 181235940,
                     'github_read_token': 'synthetic', 'dropbox_local_path': str(local),
                     'private_key_file': str(key), 'review_public_key': 'a' * 64,
                     'execution_public_key': 'b' * 64}
            path.write_text(json.dumps(value))
            path.chmod(0o600)
            with self.assertRaisesRegex(AdmissionError, 'owner-only'):
                configuration(path)
            local.chmod(0o700)
            self.assertEqual(configuration(path)['dropbox_local_path'], str(local))

    def test_valid_private_configuration_and_missing_unsafe_values(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            key = root / 'key.pem'
            key.write_bytes(pem(private_key()))
            key.chmod(0o600)
            path = root / 'config.json'
            value = {'repository': 'baphled/FlowState', 'repository_id': 1191083392,
                     'app_id': 5728089, 'installation_id': 181235940,
                     'github_read_token': 'synthetic', 'dropbox_read_token': 'synthetic',
                     'private_key_file': str(key), 'review_public_key': 'a' * 64,
                     'execution_public_key': 'b' * 64}
            path.write_text(json.dumps(value))
            path.chmod(0o600)
            self.assertEqual(configuration(path)['repository'], 'baphled/FlowState')
            for changes in ({'github_read_token': ''}, {'app_id': True},
                            {'review_public_key': 'b' * 64}, {'execution_public_key': 'bad'}):
                path.write_text(json.dumps(dict(value, **changes)))
                with self.subTest(changes=changes), self.assertRaises(AdmissionError):
                    configuration(path)
            path.chmod(0o644)
            with self.assertRaisesRegex(AdmissionError, 'owner-only'):
                configuration(path)
            with self.assertRaisesRegex(AdmissionError, 'missing'):
                configuration(root / 'absent')
