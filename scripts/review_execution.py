"""Bind trusted harness execution observations to signed review and candidate."""
import hashlib
from scripts.admission_metadata import AdmissionError, SHA
from scripts.review_signature import canonical_payload

def validate_review_scope(review, candidate, now):
    """Bind authenticated review to the decision's repository, PR, head and base."""
    if type(candidate.get("pr_number")) is not int or candidate["pr_number"] <= 0:
        raise AdmissionError("invalid trusted PR number")
    if type(review.get("pr_number")) is not int:
        raise AdmissionError("review PR number must be an integer")
    if not all(SHA.fullmatch(str(candidate.get(name, ""))) for name in ("head_sha", "base_sha")):
        raise AdmissionError("invalid trusted candidate SHA")
    names = ("repository", "pr_number", "head_sha", "base_sha")
    if any(not candidate.get(name) or review.get(name) != candidate[name] for name in names):
        raise AdmissionError("review repository, PR, head or base mismatch")
    if "admission_nonce" in candidate and review.get("admission_nonce") != candidate["admission_nonce"]:
        raise AdmissionError("review admission nonce mismatch")
    times = [review.get("issued_at"), review.get("expires_at"), now]
    if any(type(value) is not int for value in times):
        raise AdmissionError("review requires integer UTC issuance and expiry times")
    issued, expires, instant = times
    if not issued <= instant < expires or not 0 < expires - issued <= 3600:
        raise AdmissionError("review evidence is stale or has an invalid validity interval")


def validate_execution(review, execution):
    """Check authenticated harness observations, not self-declared identity strings."""
    expected = {
        "record_type": "flowstate-review-execution-v1",
        "metadata_ref": review["execution_ref"],
        "state": "completed", "verdict": "PASS",
        "review_payload_sha256": hashlib.sha256(canonical_payload(review)).hexdigest(),
        "reviewer": review["reviewer"], "implementer": review["implementer"],
    }
    if not isinstance(execution, dict) or any(execution.get(key) != value for key, value in expected.items()):
        raise AdmissionError("authenticated review execution does not match signed review")
    if any(not isinstance(item, dict) or
           (item.get("severity") in ("P0", "P1", "P2") and item.get("status") != "resolved")
           for item in review.get("findings", [])):
        raise AdmissionError("review has unresolved material findings")
    reviewer, implementer = execution["reviewer"], execution["implementer"]
    fields = ("actor_id", "session_id", "run_id")
    if any(not isinstance(run.get(key), str) or not run[key].strip()
           for run in (reviewer, implementer) for key in fields):
        raise AdmissionError("authenticated execution lacks actor, session or run identity")
    if any(reviewer[key] == implementer[key] for key in fields):
        raise AdmissionError("review execution is not independent of implementation")

