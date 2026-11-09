"""Own neutral commit metadata references and Dropbox record validation."""
import re

REFERENCE = re.compile(
    r"dropbox:/FlowState/Metadata/attempts/"
    r"[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*\.json"
)
DIGEST = re.compile(r"[0-9a-f]{64}")
REQUIRED_READS = {
    "/FlowState/Dev/00-AGENT-READ-FIRST.md",
    "/FlowState/Dev/AGENTS.md",
    "/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md",
    "/FlowState/Metadata/ACTIVE.json",
}

class AdmissionError(Exception): pass
def metadata_reference(message):
    """Extract the single neutral trailer, refusing duplicates or malformed paths."""
    refs = re.findall(r"^Project-Metadata: (.+)$", message, flags=re.M)
    if len(refs) != 1 or not REFERENCE.fullmatch(refs[0]):
        raise AdmissionError("one valid Project-Metadata reference required per commit")
    return refs[0]

def validate_identity(record, reference, parent_sha):
    """Bind an observed metadata record to one exact commit parent."""
    if not isinstance(record, dict):
        raise AdmissionError("Dropbox record must be a JSON object")
    if record.get("record_type") != "flowstate-project-metadata-v1":
        raise AdmissionError("unsupported Dropbox metadata record")
    if record.get("metadata_ref") != reference:
        raise AdmissionError("Dropbox metadata reference mismatch")
    if record.get("base_sha") != parent_sha:
        raise AdmissionError("Dropbox metadata base SHA does not match commit parent")
    if not DIGEST.fullmatch(str(record.get("docs_pack_manifest_sha256", ""))):
        raise AdmissionError("invalid documentation pack digest")


def validate_attribution(attribution):
    """Require factual attribution or an explicit unverified identity."""
    if not isinstance(attribution, dict):
        raise AdmissionError("missing observed/unverified attribution and source")
    required = ("agent", "provider", "model", "harness", "source")
    if any(not isinstance(attribution.get(key), str) or not attribution[key].strip()
           for key in required):
        raise AdmissionError("missing observed/unverified attribution and source")


def observed_paths(reads):
    """Validate the shape of the current Dropbox source-read observations."""
    if not isinstance(reads, list):
        raise AdmissionError("missing Dropbox read observations")
    paths = set()
    for item in reads:
        if not isinstance(item, dict):
            raise AdmissionError("invalid Dropbox read observation")
        path = re.sub(r"^ns:[0-9]+//", "/", str(item.get("path", "")))
        if not DIGEST.fullmatch(str(item.get("sha256", ""))) or not item.get("file_id"):
            raise AdmissionError("unverified Dropbox read observation")
        paths.add(path)
    return paths


def validate_record(record, reference, parent_sha):
    """Check one exact commit, required read observations, and its source manifest."""
    validate_identity(record, reference, parent_sha)
    validate_attribution(record.get("attribution"))
    if not REQUIRED_READS <= observed_paths(record.get("dropbox_reads")):
        raise AdmissionError("missing required current Dropbox source observations")
    manifest = (record.get("local_candidate") or {}).get("paths")
    if not isinstance(manifest, dict) or not manifest:
        raise AdmissionError("missing candidate source manifest")
    return manifest
