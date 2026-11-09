"""Metadata contract negatives: exact parent, source readset and neutral trailer."""
import unittest

from scripts.admission_metadata import AdmissionError, metadata_reference, validate_record

REF = "dropbox:/FlowState/Metadata/attempts/example-01/commit.json"
PARENT = "1" * 40
READS = (
    "/FlowState/Dev/00-AGENT-READ-FIRST.md",
    "/FlowState/Dev/AGENTS.md",
    "/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md",
    "/FlowState/Metadata/ACTIVE.json",
)


def record(**updates):
    item = {
        "record_type": "flowstate-project-metadata-v1",
        "metadata_ref": REF,
        "base_sha": PARENT,
        "docs_pack_manifest_sha256": "a" * 64,
        "attribution": {key: "unverified" for key in
                        ("agent", "provider", "model", "harness", "source")},
        "dropbox_reads": [{"path": name, "file_id": f"id:{index}",
                           "sha256": "b" * 64} for index, name in enumerate(READS)],
        "local_candidate": {"paths": {"scripts/demo.py": {
            "sha256": "c" * 64, "bytes": 4,
        }}},
    }
    item.update(updates)
    return item


class MetadataContract(unittest.TestCase):
    def test_exact_parent_and_current_reads_return_manifest(self):
        self.assertEqual(validate_record(record(), REF, PARENT),
                         record()["local_candidate"]["paths"])

    def test_missing_or_duplicated_commit_trailer_fails(self):
        for message in ("missing", "Project-Metadata: /tmp/forged.json",
                        "Project-Metadata: " + REF + "\nProject-Metadata: " + REF):
            with self.subTest(message=message):
                with self.assertRaisesRegex(AdmissionError, "one valid"):
                    metadata_reference(message)

    def test_wrong_base_and_record_reference_fail(self):
        for candidate in (record(base_sha="f" * 40), record(metadata_ref=REF + "x")):
            with self.assertRaises(AdmissionError):
                validate_record(candidate, REF, PARENT)

    def test_missing_or_malformed_read_observations_fail(self):
        bad = record()["dropbox_reads"][:2]
        for reads in (bad, {}, [{"path": READS[0], "file_id": "id:0",
                                "sha256": "not-sha"}]):
            with self.subTest(reads=reads):
                with self.assertRaises(AdmissionError):
                    validate_record(record(dropbox_reads=reads), REF, PARENT)

    def test_malformed_attribution_or_digest_fails(self):
        for candidate in (record(attribution={"agent": "unverified"}),
                          record(docs_pack_manifest_sha256="bad")):
            with self.assertRaises(AdmissionError):
                validate_record(candidate, REF, PARENT)

    def test_missing_source_manifest_fails(self):
        with self.assertRaisesRegex(AdmissionError, "source manifest"):
            validate_record(record(local_candidate={"paths": {}}), REF, PARENT)

    def test_non_object_record_fails(self):
        with self.assertRaisesRegex(AdmissionError, "JSON object"):
            validate_record([], REF, PARENT)


if __name__ == "__main__":
    unittest.main()
