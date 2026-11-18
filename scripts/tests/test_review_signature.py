"""Synthetic key fixtures and negative controls for public-key review checks."""
import hashlib
import unittest

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from scripts.admission_metadata import AdmissionError
from scripts.review_signature import canonical_payload, verify_signature


class ReviewSignature(unittest.TestCase):
    def setUp(self):
        self.private = Ed25519PrivateKey.from_private_bytes(bytes(range(32)))
        self.public = self.private.public_key().public_bytes(
            serialization.Encoding.Raw, serialization.PublicFormat.Raw).hex()
        self.review = {"repository": "owner/repo", "summary": "café ☕",
                       "pr_number": 7, "findings": [], "verdict": "PASS"}

    def sign(self, review):
        review["attestation"] = {
            "algorithm": "Ed25519",
            "key_id": hashlib.sha256(bytes.fromhex(self.public)).hexdigest(),
            "signature": self.private.sign(canonical_payload(review)).hex(),
        }
        return review

    def test_public_key_accepts_separately_signed_unicode_payload(self):
        self.assertIsNone(verify_signature(self.sign(self.review), self.public))

    def test_canonical_encoding_has_language_independent_unicode_vector(self):
        value = {"é": "☕", "a": [True, None, -2]}
        expected = (b'FLOWSTATE-REVIEW-V1\x00o2:s1:aa3:b1ni-2;'
                    b's2:\xc3\xa9s3:\xe2\x98\x95')
        self.assertEqual(canonical_payload(value), expected)

    def test_key_order_does_not_change_signing_bytes(self):
        self.assertEqual(canonical_payload({"é": 1, "z": 2}),
                         canonical_payload({"z": 2, "é": 1}))

    def test_payload_tamper_fails(self):
        review = self.sign(self.review)
        review["summary"] = "forged"
        with self.assertRaisesRegex(AdmissionError, "signature"):
            verify_signature(review, self.public)

    def test_wrong_public_key_fails(self):
        review = self.sign(self.review)
        other = Ed25519PrivateKey.from_private_bytes(bytes(range(1, 33)))
        wrong = other.public_key().public_bytes(
            serialization.Encoding.Raw, serialization.PublicFormat.Raw).hex()
        with self.assertRaises(AdmissionError):
            verify_signature(review, wrong)

    def test_missing_malformed_or_non_ascii_signatures_fail(self):
        review = self.sign(self.review)
        for signature in (None, "é" * 128, "g" * 128, "0" * 126, 4):
            with self.subTest(signature=signature):
                review["attestation"]["signature"] = signature
                with self.assertRaises(AdmissionError):
                    verify_signature(review, self.public)
        review.pop("attestation")
        with self.assertRaises(AdmissionError):
            verify_signature(review, self.public)

    def test_missing_private_or_malformed_key_is_rejected(self):
        for key in (None, "", "01", self.private.private_bytes(
                serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8,
                serialization.NoEncryption()).decode()):
            with self.subTest(key=key):
                with self.assertRaises(AdmissionError):
                    verify_signature(self.sign(self.review), key)

    def test_non_json_or_ambiguous_values_are_rejected(self):
        for value in (1.0, float("nan"), b"bytes", {1: "key"}, "\ud800"):
            with self.subTest(value=repr(value)):
                with self.assertRaises(AdmissionError):
                    canonical_payload({"value": value})

    def test_wrong_algorithm_key_id_or_envelope_fails(self):
        for name, value in (("algorithm", "HMAC"), ("key_id", "0" * 64),
                            ("extra", "ignored")):
            review = self.sign(dict(self.review))
            review["attestation"][name] = value
            with self.assertRaises(AdmissionError):
                verify_signature(review, self.public)
