"""Read-only local Dropbox adapter contract with no network access."""
import os
import tempfile
import unittest
from pathlib import Path

from scripts.admission_local_dropbox import LocalDropbox
from scripts.admission_metadata import AdmissionError


REF = "dropbox:/FlowState/Metadata/attempts/example-01/commit.json"


class LocalDropboxAdapter(unittest.TestCase):
    def test_local_dropbox_reads_records_within_private_root(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'FlowState/Metadata/attempts/example-01').mkdir(parents=True)
            (root / 'FlowState/Metadata/attempts/example-01/commit.json').write_text('{"ok":true}')
            adapter = LocalDropbox(str(root))
            self.assertEqual(adapter.record(REF), {"ok": True})

    def test_local_dropbox_fails_closed_on_escape_missing_and_oversize(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            adapter = LocalDropbox(str(root))
            for reference in (REF, "dropbox:/etc/passwd", "dropbox:/FlowState/../../etc/passwd",
                              "not-a-reference"):
                with self.subTest(reference=reference), self.assertRaises(AdmissionError):
                    adapter.record(reference)
            target = root / 'FlowState/Metadata/attempts/example-01'
            target.mkdir(parents=True)
            (target / 'commit.json').write_text('{"bad":')
            with self.assertRaises(AdmissionError):
                adapter.record(REF)
            (target / 'commit.json').write_text('"' + 'x' * 131072 + '"')
            with self.assertRaises(AdmissionError):
                adapter.record(REF)

    def test_local_dropbox_requires_private_root(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            os.chmod(root, 0o755)
            try:
                with self.assertRaisesRegex(AdmissionError, 'owner-only'):
                    LocalDropbox(str(root))
            finally:
                os.chmod(root, 0o700)


if __name__ == "__main__":
    unittest.main()
