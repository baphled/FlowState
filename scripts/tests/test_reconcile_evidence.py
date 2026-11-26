import hashlib
import json
import unittest
from dataclasses import replace
from datetime import datetime, timedelta, timezone
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_evidence import canonical_bytes, file_name, load, seal, verify
from scripts.reconcile_target import ResolvedTarget

A, B, C = "a" * 40, "b" * 40, "c" * 40
P, Q = "1" * 64, "2" * 64
NOW = datetime(2026, 11, 26, 12, 0, 0, tzinfo=timezone.utc)
TTL = 900
PACKET = "PLAN-X-001/EVIDENCE1"
EXPECTED = {"checkpoint": "pre-push", "candidate_sha": C, "packet_id": PACKET,
            "packet_sha256": P, "plan_sha256": Q}


def stamp(delta=0):
    return (NOW + timedelta(seconds=delta)).isoformat(timespec="seconds")


def fields(**changes):
    base = {"checkpoint": "pre-push", "repository": "acme/widget", "ref": "refs/heads/dev",
            "previous_sha": A, "resolved_sha": B, "candidate_sha": C, "packet_id": PACKET,
            "packet_sha256": P, "plan_sha256": Q, "upstream_paths": ["docs/a.md", "scripts/\u00e9.py"],
            "outcome": "non_material", "reasons": ["TARGET_ADVANCED"], "observed_at": stamp()}
    return dict(base, **changes)


def live(**changes):
    return replace(ResolvedTarget("acme/widget", "refs/heads/dev", B, stamp(), ("api",)), **changes)


def raw_of(**changes):
    return canonical_bytes(seal(fields(**changes)))


def check(raw=None, expected=None, target=None, ttl=TTL):
    return verify(raw_of() if raw is None else raw, expected or EXPECTED, target or live(), ttl, NOW)


class SealTest(unittest.TestCase):
    def test_digest_is_sha256_of_the_canonical_bytes_without_the_digest_field(self):
        record = seal(fields())
        body = {k: v for k, v in record.items() if k != "digest"}
        wire = json.dumps(body, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("ascii")
        self.assertEqual(record["digest"], hashlib.sha256(wire).hexdigest())

    def test_record_type_is_set_and_bytes_are_sorted_compact_ascii(self):
        wire = raw_of().decode("ascii")
        self.assertEqual((wire.startswith('{"candidate_sha":"' + C), " " in wire, '"scripts/\\u00e9.py"' in wire,
                          '"record_type":"flowstate-reconciliation-v1"' in wire), (True, False, True, True))

    def test_file_name_uses_checkpoint_and_first_twelve_digest_characters(self):
        record = seal(fields())
        self.assertEqual(file_name(record), "reconciliation-pre-push-" + record["digest"][:12] + ".json")

    def test_malformed_fields_are_refused(self):
        bad = (dict(outcome="bogus"), dict(reasons=["lower"]), dict(previous_sha="abc"), dict(packet_sha256="A" * 64),
               dict(checkpoint="Bad Name"), dict(checkpoint="../x"), dict(upstream_paths=["x"] * 201),
               dict(upstream_paths=[1]), dict(extra=1), dict(observed_at="2026-13-45T00:00:00+00:00"),
               dict(observed_at="2026-11-26T12:00:00"), dict(repository="no-slash"), dict(ref="dev"))
        for changes in bad:
            with self.subTest(changes=changes), self.assertRaises(ReconcileError):
                seal(fields(**changes))

    def test_missing_key_is_refused(self):
        data = fields()
        del data["reasons"]
        with self.assertRaises(ReconcileError):
            seal(data)


class AcceptTest(unittest.TestCase):
    def test_fresh_matching_record_is_accepted_and_returned(self):
        self.assertEqual(check()["resolved_sha"], B)

    def test_age_exactly_at_the_ttl_and_future_exactly_at_the_bound_are_accepted(self):
        for delta in (-TTL, 300):
            with self.subTest(delta=delta):
                self.assertEqual(check(raw_of(observed_at=stamp(delta)))["checkpoint"], "pre-push")

    def test_load_returns_the_record_without_live_context(self):
        self.assertEqual(load(raw_of())["packet_id"], PACKET)


class RejectTest(unittest.TestCase):
    def refused(self, pattern, *args, **kwargs):
        with self.assertRaisesRegex(ReconcileError, pattern):
            check(*args, **kwargs)

    def test_older_than_ttl_is_stale(self):
        self.refused("stale", raw_of(observed_at=stamp(-TTL - 1)))

    def test_more_than_300_seconds_in_the_future_is_refused(self):
        self.refused("future", raw_of(observed_at=stamp(301)))

    def test_other_candidate_packet_plan_checkpoint_or_packet_id_is_refused(self):
        for key, other in (("candidate_sha", A), ("packet_sha256", Q), ("plan_sha256", P),
                           ("checkpoint", "dispatch"), ("packet_id", "PLAN-X-001/OTHER")):
            with self.subTest(key=key):
                self.refused("evidence %s does not match" % key, expected=dict(EXPECTED, **{key: other}))

    def test_other_repository_or_ref_is_refused(self):
        for key, target in (("repository", live(repo="evil/widget")), ("ref", live(ref="refs/heads/other"))):
            with self.subTest(key=key):
                self.refused("evidence %s does not match" % key, target=target)

    def test_target_advanced_after_evaluation_is_refused_as_such(self):
        self.refused("target advanced", target=live(sha=A))

    def test_identity_is_checked_before_target_advance(self):
        self.refused("evidence repository", target=live(repo="evil/widget", sha=A))

    def test_expected_identity_must_name_exactly_the_five_keys(self):
        for expected in ({k: v for k, v in EXPECTED.items() if k != "checkpoint"}, dict(EXPECTED, extra="x")):
            with self.subTest(expected=sorted(expected)):
                self.refused("expected", expected=expected)

    def test_ttl_must_be_a_positive_integer(self):
        for ttl in (0, -1, True, "900", None):
            with self.subTest(ttl=ttl):
                self.refused("ttl", ttl=ttl)


class CanonicalTest(unittest.TestCase):
    def refused(self, raw, pattern):
        with self.assertRaisesRegex(ReconcileError, pattern):
            load(raw)

    def test_non_canonical_serialisations_are_refused_even_with_a_valid_digest(self):
        record = seal(fields())
        variants = (json.dumps(record, indent=1).encode(), json.dumps(record, sort_keys=True).encode(),
                    canonical_bytes(record) + b"\n", json.dumps(record, sort_keys=True, separators=(",", ":"),
                                                                ensure_ascii=False).encode())
        for raw in variants:
            with self.subTest(raw=raw[:30]):
                self.refused(raw, "not canonical")

    def test_changed_field_with_stale_digest_is_refused(self):
        record = seal(fields())
        record["outcome"] = "material"
        self.refused(canonical_bytes(record), "digest")

    def test_extra_or_missing_key_is_refused(self):
        extra = dict(seal(fields()), extra=1)
        missing = {k: v for k, v in seal(fields()).items() if k != "reasons"}
        for record in (extra, missing):
            with self.subTest(keys=len(record)):
                self.refused(json.dumps(record, sort_keys=True, separators=(",", ":")).encode(), "keys")

    def test_unreadable_input_is_refused(self):
        for raw in (b'{"a":1,"a":2}', b"\xff\xfe", b"[]", b"", b" " * 131073, "text"):
            with self.subTest(raw=raw[:12]), self.assertRaises(ReconcileError):
                load(raw)


if __name__ == "__main__":
    unittest.main()
