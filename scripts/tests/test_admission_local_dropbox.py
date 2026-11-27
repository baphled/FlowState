"""Read-only local Dropbox adapter contract with no network access."""
import hashlib
import os
import re
import tempfile
import unittest
from pathlib import Path

from scripts import admission_local_dropbox as local_dropbox
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


class TrustedClosure(unittest.TestCase):
    """The gate file list is closed over imports; the byte reader must not drag in the runner's I/O."""

    def test_the_byte_reader_does_not_import_the_runner_io_module(self):
        source = Path(local_dropbox.__file__).read_text(encoding="utf-8")
        imported = re.findall(r"^\s*(?:from|import)\s+(?:scripts\.)?([A-Za-z_][A-Za-z0-9_]*)", source, flags=re.M)
        self.assertNotIn("admission_io", imported,
                         "admission_local_dropbox must take REFERENCE from admission_metadata, not admission_io")
        self.assertIs(local_dropbox.REFERENCE, __import__("scripts.admission_metadata", fromlist=["REFERENCE"]).REFERENCE)


class ByteReader(unittest.TestCase):
    """read_bytes and sha256_under: three prefixes, no symlinks, 1 MiB bound."""
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.adapter = LocalDropbox(str(self.root))

    def put(self, relative, data=b'bytes'):
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        return path

    def test_reads_bytes_under_each_permitted_prefix(self):
        for path in ('/FlowState/Dev/governance/a.md', '/FlowState/Knowledge/Plans/PLAN.md',
                     '/FlowState/Metadata/ACTIVE.json'):
            with self.subTest(path=path):
                self.put(path.lstrip('/'), path.encode())
                self.assertEqual(self.adapter.read_bytes(path), path.encode())

    def test_sha256_under_hashes_the_stored_bytes(self):
        self.put('FlowState/Dev/AGENTS.md', b'live')
        expected = hashlib.sha256(b'live').hexdigest()
        self.assertEqual(local_dropbox.sha256_under(str(self.root), 'FlowState/Dev/AGENTS.md'), expected)

    def test_paths_outside_the_three_prefixes_are_refused(self):
        self.put('FlowState/Other/x.md')
        self.put('FlowState/Dev/x.md')
        for path in ('/etc/passwd', '/FlowState/Other/x.md', '/FlowState/Dev', '/FlowState/Dev/',
                     '/FlowState/Knowledge/x.md', '/FlowState/Dev/../Other/x.md',
                     '/FlowState/Dev//x.md', '/FlowState/Dev/./x.md', 'FlowStateDev/x.md'):
            with self.subTest(path=path), self.assertRaisesRegex(AdmissionError, 'not a readable'):
                self.adapter.read_bytes(path)

    def test_a_symlinked_component_is_refused(self):
        outside = self.put('elsewhere/AGENTS.md', b'outside').parent
        (self.root / 'FlowState').mkdir()
        (self.root / 'FlowState/Dev').symlink_to(outside)
        self.put('FlowState/Metadata/real.json')
        (self.root / 'FlowState/Metadata/link.json').symlink_to(self.root / 'FlowState/Metadata/real.json')
        for path in ('/FlowState/Dev/AGENTS.md', '/FlowState/Metadata/link.json'):
            with self.subTest(path=path), self.assertRaisesRegex(AdmissionError, path):
                self.adapter.read_bytes(path)

    def test_the_file_bound_is_one_mebibyte(self):
        self.put('FlowState/Dev/exact.md', b'x' * 1048576)
        self.put('FlowState/Dev/over.md', b'x' * 1048577)
        self.assertEqual(len(self.adapter.read_bytes('/FlowState/Dev/exact.md')), 1048576)
        with self.assertRaisesRegex(AdmissionError, '/FlowState/Dev/over.md'):
            self.adapter.read_bytes('/FlowState/Dev/over.md')

    def test_missing_and_non_regular_files_are_refused_naming_the_path(self):
        (self.root / 'FlowState/Dev/directory').mkdir(parents=True)
        os.mkfifo(self.root / 'FlowState/Dev/fifo')
        for path in ('/FlowState/Dev/absent.md', '/FlowState/Dev/directory', '/FlowState/Dev/fifo'):
            with self.subTest(path=path), self.assertRaisesRegex(AdmissionError, path):
                self.adapter.read_bytes(path)

GOVERNANCE = ["/FlowState/Dev/00-AGENT-READ-FIRST.md", "/FlowState/Dev/AGENTS.md",
              "/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md"]
ACTIVE = "/FlowState/Metadata/ACTIVE.json"
LIVE = {path: format(index, "x") * 64 for index, path in enumerate(GOVERNANCE + [ACTIVE], 1)}
PACK = "c" * 64


def evidence(**changes):
    reads = [{"path": path, "sha256": digest, "file_id": "id"} for path, digest in LIVE.items()]
    return dict({"docs_pack_manifest_sha256": PACK, "dropbox_reads": reads}, **changes)


def live_sha256(path):
    if path not in LIVE:
        raise AdmissionError("Dropbox file missing or unreadable: " + path)
    return LIVE[path]


class ReadEvidence(unittest.TestCase):
    def verify(self, record):
        return local_dropbox.verify_read_evidence(record, live_sha256, PACK)

    def test_a_record_with_the_live_hashes_is_accepted(self):
        self.assertIsNone(self.verify(evidence()))

    def test_one_stale_hash_is_refused_naming_the_path_and_the_remedy(self):
        record = evidence()
        record["dropbox_reads"][1]["sha256"] = "0" * 64
        with self.assertRaisesRegex(AdmissionError, GOVERNANCE[1]) as caught:
            self.verify(record)
        self.assertIn("rebuild the external record", str(caught.exception))
        self.assertIn("no re-review", str(caught.exception))

    def test_a_pack_digest_that_differs_from_active_is_refused(self):
        with self.assertRaisesRegex(AdmissionError, "docs_pack_manifest_sha256.*rebuild"):
            self.verify(evidence(docs_pack_manifest_sha256="d" * 64))

    def test_a_changed_active_json_hash_is_still_accepted(self):
        record = evidence()
        record["dropbox_reads"][3]["sha256"] = "0" * 64
        self.assertIsNone(self.verify(record))

    def test_a_namespaced_path_and_a_duplicate_stale_entry_are_compared(self):
        record = evidence()
        record["dropbox_reads"][0]["path"] = "ns:123/" + GOVERNANCE[0]
        self.assertIsNone(self.verify(record))
        record["dropbox_reads"].append({"path": GOVERNANCE[0], "sha256": "0" * 64, "file_id": "id"})
        with self.assertRaisesRegex(AdmissionError, GOVERNANCE[0]):
            self.verify(record)

    def test_a_missing_governance_read_and_an_unreadable_file_are_refused(self):
        with self.assertRaisesRegex(AdmissionError, GOVERNANCE[2]):
            self.verify(evidence(dropbox_reads=evidence()["dropbox_reads"][:2]))
        with self.assertRaisesRegex(AdmissionError, "unreadable"):
            local_dropbox.verify_read_evidence(evidence(), lambda path: live_sha256("/absent"), PACK)


if __name__ == "__main__":
    unittest.main()
