"""Own parent-bound commit metadata references and record validation."""
import re

REFERENCE = re.compile(
    r"dropbox:/FlowState/Metadata/attempts/"
    r"[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*\.json"
)
SHA = re.compile(r"[0-9a-f]{40}")
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

def validate_parent(record, reference, parent_sha):
    """Validate externally downloaded record against the exact commit parent."""
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

def validate_record(record, reference, parent_sha):
    """Check parent-bound content, attribution, reads and source manifest."""
    validate_parent(record, reference, parent_sha)
    attribution = record.get("attribution")
    if not isinstance(attribution, dict) or any(
        not isinstance(attribution.get(k), str) or not attribution[k].strip()
        for k in ("agent", "provider", "model", "harness", "source")
    ):
        raise AdmissionError("missing observed/unverified attribution and source")
    reads = record.get("dropbox_reads")
    if not isinstance(reads, list):
        raise AdmissionError("missing Dropbox read observations")
    observed = set()
    for item in reads:
        if not isinstance(item, dict):
            raise AdmissionError("invalid Dropbox read observation")
        path = str(item.get("path", ""))
        path = re.sub(r"^ns:[0-9]+//", "/", path)
        if not DIGEST.fullmatch(str(item.get("sha256", ""))) or not item.get("file_id"):
            raise AdmissionError("unverified Dropbox read observation")
        observed.add(path)
    if not REQUIRED_READS <= observed:
        raise AdmissionError("missing required current Dropbox source observations")
    manifest = (record.get("local_candidate") or {}).get("paths")
    if not isinstance(manifest, dict) or not manifest:
        raise AdmissionError("missing candidate source manifest")
    return manifest


def event_pr_number(event):
    """Use the PR number from trusted GitHub event payload, never an arbitrary URL."""
    number = (event.get("pull_request") or {}).get("number")
    if number is None and (event.get("issue") or {}).get("pull_request"):
        number = event["issue"].get("number")
    if number is None:
        number = (event.get("inputs") or {}).get("pull_request")
        if isinstance(number, str) and number.isascii() and number.isdecimal():
            number = int(number)
    if not isinstance(number, int) or isinstance(number, bool) or number <= 0:
        raise AdmissionError("event has no valid PR number")
    return number

