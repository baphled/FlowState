"""Validate claimed agent review; signed attestation is a separate qualification."""
import re
from scripts.admission_metadata import AdmissionError, REFERENCE

def agent_review_reference(body):
    refs = re.findall(r"^Agent-Review: (.+)$", body if isinstance(body, str) else "", flags=re.M)
    if len(refs) != 1 or not REFERENCE.fullmatch(refs[0]):
        raise AdmissionError("one external agent-review record required in PR description")
    return refs[0]

def review_binding(review, reference, head_sha, number):
    if not isinstance(review, dict) or review.get("record_type") != "flowstate-agent-review-v1":
        raise AdmissionError("missing or invalid independent agent review")
    if review.get("metadata_ref") != reference or review.get("head_sha") != head_sha:
        raise AdmissionError("agent review does not bind the exact candidate")
    if review.get("pr_number") != number or review.get("verdict") != "PASS":
        raise AdmissionError("agent review is not an accepted verdict for this PR")

def review_runs(review):
    reviewer, implementer = review.get("reviewer"), review.get("implementer")
    if not isinstance(reviewer, dict) or not isinstance(implementer, dict):
        raise AdmissionError("missing agent run identities")
    reviewer_run, implementer_run = reviewer.get("run_id"), implementer.get("run_id")
    if (reviewer.get("role") != "verifier" or
        not isinstance(reviewer_run, str) or not reviewer_run.strip() or
        not isinstance(implementer_run, str) or not implementer_run.strip() or
        reviewer_run == implementer_run or
        not reviewer.get("identity_evidence") or not implementer.get("identity_evidence")):
        raise AdmissionError("agent review does not establish separate observed runs")

def validate_agent_review(review, reference, head_sha, number):
    """Check claim structure; do not treat it as authenticated actor provenance."""
    review_binding(review, reference, head_sha, number)
    review_runs(review)
    oracle = review.get("negative_oracle")
    if (not isinstance(oracle, dict) or oracle.get("result") != "PASS" or
        not isinstance(oracle.get("evidence"), str) or not oracle["evidence"].strip()):
        raise AdmissionError("independent negative-oracle evidence required")
    if not isinstance(review.get("findings"), list) or not str(review.get("summary") or "").strip():
        raise AdmissionError("substantive agent findings and summary required")

