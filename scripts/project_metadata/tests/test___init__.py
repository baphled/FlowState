"""Owning tests for the trailer grammar and referenced-record readability."""
import json
import os
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from scripts.project_metadata import message_errors

REF = 'dropbox:/FlowState/Metadata/attempts/synthetic-fixture/record.json'


def with_root(root, message):
    env = {**os.environ, 'FLOWSTATE_DROPBOX_ROOT': str(root)}
    with mock.patch.dict(os.environ, env, clear=True):
        return message_errors(message)


class SquashTipTrailerGuard(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.record = self.root / 'FlowState/Metadata/attempts/attempt/record.json'
        self.record.parent.mkdir(parents=True)
        self.record.write_text(json.dumps({'record_type': 'flowstate-project-metadata-v1'}))
        self.valid = 'dropbox:/FlowState/Metadata/attempts/attempt/record.json'

    def test_exactly_one_valid_existing_readable_trailer_passes(self):
        self.assertEqual(with_root(self.root, 'fix: x\n\nProject-Metadata: ' + self.valid), [])

    def test_zero_trailers_fails(self):
        errors = with_root(self.root, 'fix: explicit trailer-less body')
        self.assertTrue(any('one valid Project-Metadata' in e for e in errors))

    def test_two_trailers_fails(self):
        two = 'fix: x\n\nProject-Metadata: ' + self.valid + \
              '\nProject-Metadata: ' + self.valid
        self.assertTrue(any('multiple' in e for e in with_root(self.root, two)))

    def test_dangling_reference_fails_when_mount_is_configured(self):
        dangling = 'dropbox:/FlowState/Metadata/attempts/attempt/gone.json'
        errors = with_root(self.root, 'fix: x\n\nProject-Metadata: ' + dangling)
        self.assertTrue(any('missing on the Dropbox mount' in e for e in errors))

    def test_unreadable_record_fails_when_mount_is_configured(self):
        self.record.write_text('{not json')
        errors = with_root(self.root, 'fix: x\n\nProject-Metadata: ' + self.valid)
        self.assertTrue(any('not readable JSON' in e for e in errors))

    def test_structural_check_stands_when_no_mount_is_configured(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            self.assertEqual(message_errors('fix: x\n\nProject-Metadata: ' + REF), [])


if __name__ == '__main__':
    unittest.main()
