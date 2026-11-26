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


class ShapeValidatorUnchanged(unittest.TestCase):
    def test_the_shape_validator_still_accepts_a_stale_but_well_formed_record(self):
        live = {"/FlowState/Dev/00-AGENT-READ-FIRST.md", "/FlowState/Dev/AGENTS.md",
                "/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md", "/FlowState/Metadata/ACTIVE.json"}
        stale = {"record_type": "flowstate-project-metadata-v1", "metadata_ref": REF, "base_sha": "1" * 40,
                 "docs_pack_manifest_sha256": "c" * 64,
                 "attribution": {key: "unverified" for key in ("agent", "provider", "model", "harness", "source")},
                 "local_candidate": {"paths": {"demo": {"sha256": "e" * 64, "bytes": 4}}},
                 "dropbox_reads": [{"path": path, "sha256": "0" * 64, "file_id": "id"} for path in live]}
        self.assertEqual(validate_record(stale, REF, "1" * 40), {"demo": {"sha256": "e" * 64, "bytes": 4}})
