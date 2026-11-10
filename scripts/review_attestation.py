"""Verify signed reviewer evidence; signing authority is external to FlowState."""
import hashlib
import hmac
import json
import re

ISSUER = "flowstate-independent-review-attestor-v1"
DOMAIN = b"flowstate-agent-review-v1\0"


class ReviewAttestationError(ValueError):
    """An agent review was not authenticated by the configured attestor."""


def verify_attestation(review, key):
    """Authenticate the full review record using an isolated attestor secret."""
    if not isinstance(key, str) or len(key) < 32:
        raise ReviewAttestationError("trusted review attestation key not configured")
    if not isinstance(review, dict):
        raise ReviewAttestationError("review attestation requires a JSON object")
    proof = review.get("attestation")
    if not isinstance(proof, dict) or proof.get("issuer") != ISSUER:
        raise ReviewAttestationError("missing independently signed agent review")
    signature = proof.get("hmac_sha256")
    if not isinstance(signature, str) or not re.fullmatch(r"[0-9a-f]{64}", signature):
        raise ReviewAttestationError("invalid review signature")
    try:
        canonical = json.dumps(
            {k: v for k, v in review.items() if k != "attestation"},
            sort_keys=True, separators=(",", ":"), allow_nan=False,
        ).encode("utf-8")
        expected = hmac.new(key.encode("utf-8"), DOMAIN + canonical, hashlib.sha256).hexdigest()
    except (TypeError, ValueError, UnicodeError) as error:
        raise ReviewAttestationError("invalid review payload") from error
    if not hmac.compare_digest(signature, expected):
        raise ReviewAttestationError("agent review attestation failed authentication")
