"""Negative tests for the parent-bound commit metadata record."""
import unittest
from scripts.admission_metadata import AdmissionError, metadata_reference, validate_record

REF = "dropbox:/FlowState/Metadata/attempts/example-01/commit.json"

class MetadataContract(unittest.TestCase):
    def test_trailer_duplicates_fail(self):
        with self.assertRaisesRegex(AdmissionError, "one valid"):
            metadata_reference("Project-Metadata: " + REF + "\nProject-Metadata: " + REF)
    def test_wrong_parent_fails(self):
        with self.assertRaises(AdmissionError):
            validate_record({"record_type": "flowstate-project-metadata-v1", "metadata_ref": REF,
                             "base_sha": "2"*40}, REF, "1"*40)
