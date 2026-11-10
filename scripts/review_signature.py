"""Verify reviewer-attestor signatures using public keys, never signing secrets."""
import hashlib
import re

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
from scripts.admission_metadata import AdmissionError

DOMAIN = b"FLOWSTATE-REVIEW-V1\x00"


def canonical_value(value):
    """Encode the restricted JSON value grammar specified in REVIEW_SIGNATURE.md."""
    if value is None:
        return b"n"
    if isinstance(value, bool):
        return b"b1" if value else b"b0"
    if isinstance(value, int):
        return b"i" + str(value).encode("ascii") + b";"
    if isinstance(value, str):
        raw = value.encode("utf-8", errors="strict")
        return b"s" + str(len(raw)).encode("ascii") + b":" + raw
    if isinstance(value, list):
        return b"a" + str(len(value)).encode("ascii") + b":" + b"".join(
            map(canonical_value, value))
    if isinstance(value, dict) and all(isinstance(key, str) for key in value):
        keys = sorted(value, key=lambda key: key.encode("utf-8", errors="strict"))
        return b"o" + str(len(keys)).encode("ascii") + b":" + b"".join(
            canonical_value(key) + canonical_value(value[key]) for key in keys)
    raise AdmissionError("review payload must contain strings, integers, booleans, null, arrays or objects")


def canonical_payload(review):
    """Bind every top-level review field except its attestation envelope."""
    if not isinstance(review, dict):
        raise AdmissionError("review signature requires an object")
    try:
        payload = {key: value for key, value in review.items() if key != "attestation"}
        return DOMAIN + canonical_value(payload)
    except (UnicodeError, ValueError, OverflowError, RecursionError) as error:
        raise AdmissionError("invalid canonical review payload") from error


def decode_hex(value, length, description):
    """Reject noncanonical keys/signatures before calling the crypto library."""
    if not isinstance(value, str) or not re.fullmatch(f"[0-9a-f]{{{length * 2}}}", value):
        raise AdmissionError("missing or invalid " + description)
    return bytes.fromhex(value)


def verify_signature(review, public_key):
    """Authenticate attestor key possession; independent execution is checked separately."""
    public = decode_hex(public_key, 32, "Ed25519 public verification key")
    if not isinstance(review, dict):
        raise AdmissionError("review signature requires an object")
    envelope = review.get("attestation")
    if not isinstance(envelope, dict) or set(envelope) != {"algorithm", "key_id", "signature"}:
        raise AdmissionError("missing or malformed review signature envelope")
    if envelope["algorithm"] != "Ed25519" or envelope["key_id"] != hashlib.sha256(public).hexdigest():
        raise AdmissionError("review signature issuer or algorithm mismatch")
    signature = decode_hex(envelope["signature"], 64, "review signature")
    try:
        Ed25519PublicKey.from_public_bytes(public).verify(signature, canonical_payload(review))
    except (InvalidSignature, ValueError) as error:
        raise AdmissionError("review signature authentication failed") from error
