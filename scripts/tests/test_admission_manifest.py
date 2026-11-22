"""Exercise the evidence-manifest entry shape, alone and through record validation."""
import unittest

from scripts.admission_manifest import manifest_entry_valid
from scripts.admission_metadata import AdmissionError, validate_record
from scripts.tests.test_admission_io import PARENT, record


class ManifestEntries(unittest.TestCase):
    def accepted(self, entry):
        evidence = record()
        evidence["local_candidate"]["paths"]["gone.py"] = entry
        return validate_record(evidence, record()["metadata_ref"], PARENT)
    def test_exact_deletion_entry_is_accepted(self):
        self.assertEqual(self.accepted({"deleted": True})["gone.py"], {"deleted": True})
    def test_malformed_deletion_entries_are_rejected(self):
        for entry in ({"deleted": False}, {"deleted": "true"}, {"deleted": 1}, {"deleted": None},
                      {"deleted": True, "sha256": "a" * 64, "bytes": 1}, {"deleted": True, "bytes": 0}):
            with self.subTest(entry=entry):
                with self.assertRaisesRegex(AdmissionError, "manifest entry"):
                    self.accepted(entry)
    def test_non_object_manifest_entry_is_rejected(self):
        for entry in ({}, [], "x", None, True):
            with self.subTest(entry=entry):
                with self.assertRaisesRegex(AdmissionError, "manifest entry"):
                    self.accepted(entry)

    def test_blob_entry_shape_is_unchanged(self):
        self.assertTrue(manifest_entry_valid("a.py", {"sha256": "a" * 64, "bytes": 0}))
    def test_blob_entry_with_extra_key_or_bad_size_is_rejected(self):
        for entry in ({"sha256": "a" * 64, "bytes": 1, "x": 1}, {"sha256": "a" * 64, "bytes": True},
                      {"sha256": "a" * 64, "bytes": -1}, {"sha256": "A" * 64, "bytes": 1}, {"sha256": "a" * 64},
                      {"sha256": int("1" * 64), "bytes": 1}):
            with self.subTest(entry=entry):
                self.assertFalse(manifest_entry_valid("a.py", entry))


if __name__ == "__main__":
    unittest.main()
