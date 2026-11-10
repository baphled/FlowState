"""Require separately signed observer evidence rather than raw Dropbox claims."""
import unittest

from scripts.admission_authentication import authenticate_review
from scripts.admission_metadata import AdmissionError
from scripts.tests.test_admission_io import (
    REVIEW_PRIVATE, EXECUTION_PRIVATE, REVIEW_REF, EXECUTION_REF,
    FakeDropbox, agent_review, public, sign,
)


class ReviewAuthentication(unittest.TestCase):
    def authenticate(self, read, review_key=None, execution_key=None):
        review = agent_review()
        candidate = {name:review[name] for name in ("repository","pr_number","head_sha","base_sha")}
        return authenticate_review(review, REVIEW_REF, candidate,
            public(REVIEW_PRIVATE) if review_key is None else review_key,
            public(EXECUTION_PRIVATE) if execution_key is None else execution_key,read,150)

    def test_review_and_separately_signed_execution_succeed(self):
        read = FakeDropbox().record
        self.assertIsNone(self.authenticate(read))

    def test_missing_or_identical_public_authorities_fail(self):
        for review_key, execution_key in [("",public(EXECUTION_PRIVATE)),
                                          (public(REVIEW_PRIVATE),""),
                                          (public(REVIEW_PRIVATE),public(REVIEW_PRIVATE))]:
            with self.subTest(keys=(review_key,execution_key)):
                with self.assertRaisesRegex(AdmissionError,"distinct"):
                    self.authenticate(FakeDropbox().record,review_key,execution_key)

    def test_unsigned_tampered_or_wrong_authority_execution_fails(self):
        for mode in ("unsigned","tampered","wrong authority"):
            execution=FakeDropbox().record(EXECUTION_REF)
            if mode=="unsigned": execution.pop("attestation")
            if mode=="tampered": execution["reviewer"]["actor_id"]="forged"
            if mode=="wrong authority": sign(execution,REVIEW_PRIVATE)
            with self.subTest(mode=mode):
                with self.assertRaises(AdmissionError):
                    self.authenticate(lambda reference:execution)

    def test_unavailable_execution_authority_fails(self):
        def unavailable(reference):
            raise AdmissionError("authority unavailable")
        with self.assertRaisesRegex(AdmissionError,"authority unavailable"):
            self.authenticate(unavailable)
