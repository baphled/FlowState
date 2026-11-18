"""Authenticate review execution observations with a separately pinned authority."""
from scripts.admission_metadata import AdmissionError
from scripts.admission_review import authenticate_agent_review
from scripts.review_signature import verify_signature


def authenticate_review(review, reference, candidate, review_key, execution_key, read, now):
    """Require two distinct public keys and track the signed execution read.

    The execution key must belong to an authority observing real harness runs;
    possession of either signing key alone does not establish independent work.
    """
    if not review_key or not execution_key or review_key == execution_key:
        raise AdmissionError("distinct review and execution public authorities required")
    def authenticated_execution(execution_ref):
        execution = read(execution_ref)
        verify_signature(execution, execution_key)
        return execution
    authenticate_agent_review(review, reference, candidate, review_key,
                              authenticated_execution, now)
