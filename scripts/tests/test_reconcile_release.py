import copy
import unittest
from scripts.reconcile_config import parse_config
from scripts.reconcile_release import (check_candidate, check_checksums, check_published, check_version,
                                       verify_after, verify_before)

A, B = "a" * 40, "b" * 40
D1, D2 = "1" * 64, "2" * 64
BASE = {
    "repo": "acme/widget",
    "remote": {"name": "origin", "url": "https://example.test/acme/widget.git"},
    "integration_branch": "dev",
    "release_branch": "stable",
    "resolve_modes": {"hook": ["ls-remote", "api"], "ci": ["ls-remote", "api"],
                      "runner": ["api"], "merge_script": ["api"]},
}
ASSETS = ("widget-linux", "widget-windows.exe")
BUILD = {"widget-linux": D1, "widget-windows.exe": D2}


def config():
    return parse_config(copy.deepcopy(BASE))


class FakeTransport:
    def __init__(self, tip=A, identity="acme/widget", assets=ASSETS):
        self.tip, self.identity, self.assets = tip, identity, assets
        self.calls = []

    def _value(self, value):
        if isinstance(value, Exception):
            raise value
        return value

    def api(self, repo, branch):
        self.calls.append(("api", repo, branch))
        return self.identity, self._value(self.tip)

    def release_assets(self, repo, tag):
        self.calls.append(("assets", repo, tag))
        return self._value(self.assets)


class CandidateTest(unittest.TestCase):
    def test_candidate_equal_to_the_tip_is_allowed(self):
        self.assertTrue(check_candidate(config(), A, FakeTransport()).allowed)

    def test_candidate_that_differs_from_the_tip_is_blocked(self):
        decision = check_candidate(config(), B, FakeTransport())
        self.assertEqual((decision.allowed, decision.reason), (False, "candidate-not-tip"))

    def test_the_configured_repository_and_release_branch_are_asked(self):
        transport = FakeTransport()
        check_candidate(config(), A, transport)
        self.assertEqual(transport.calls, [("api", "acme/widget", "stable")])

    def test_unresolvable_or_wrong_or_malformed_tip_is_blocked(self):
        for name, candidate, transport in (("tip-unresolved", A, FakeTransport(tip=OSError("down"))),
                                           ("repository-identity-mismatch", A, FakeTransport(identity="evil/widget")),
                                           ("tip-malformed", A, FakeTransport(tip="abc")),
                                           ("candidate-malformed", "A" * 40, FakeTransport())):
            with self.subTest(name=name):
                self.assertEqual(check_candidate(config(), candidate, transport).reason, name)


class VersionTest(unittest.TestCase):
    def test_tag_matching_the_computed_version_is_allowed(self):
        self.assertTrue(all(check_version(tag, "1.4.0").allowed for tag in ("v1.4.0", "1.4.0")))

    def test_tag_that_differs_from_the_computed_version_is_blocked(self):
        decision = check_version("v1.4.1", "1.4.0")
        self.assertEqual((decision.allowed, decision.reason), (False, "tag-version-mismatch"))

    def test_malformed_tag_or_version_is_blocked(self):
        for name, tag, version in (("tag-malformed", "vv1.4.0", "1.4.0"), ("tag-malformed", None, "1.4.0"),
                                   ("computed-version-malformed", "v1.4.0", ""),
                                   ("computed-version-malformed", "v1.4", "1.4")):
            with self.subTest(tag=tag, version=version):
                self.assertEqual(check_version(tag, version).reason, name)


class ChecksumTest(unittest.TestCase):
    def test_identical_checksums_are_allowed(self):
        self.assertTrue(check_checksums(BUILD, dict(BUILD)).allowed)

    def test_corrupted_checksum_is_blocked_and_names_the_artefact(self):
        decision = check_checksums(BUILD, dict(BUILD, **{"widget-linux": D2}))
        self.assertEqual((decision.allowed, decision.reason), (False, "checksum-mismatch: widget-linux"))

    def test_different_artefact_set_is_blocked(self):
        for observed in ({"widget-linux": D1}, dict(BUILD, extra=D1)):
            with self.subTest(observed=sorted(observed)):
                self.assertEqual(check_checksums(BUILD, observed).reason, "artefact-set-mismatch")

    def test_empty_or_malformed_checksums_are_blocked(self):
        for name, expected, observed in (("build-checksums-invalid", {}, BUILD),
                                         ("build-checksums-invalid", None, BUILD),
                                         ("artefact-checksums-invalid", BUILD, {}),
                                         ("artefact-checksums-invalid", BUILD, {"widget-linux": "xyz", "widget-windows.exe": D2})):
            with self.subTest(name=name, observed=observed):
                self.assertEqual(check_checksums(expected, observed).reason, name)


class PublishedTest(unittest.TestCase):
    def test_release_carrying_every_expected_asset_is_allowed(self):
        transport = FakeTransport(assets=list(ASSETS) + ["notes"])
        self.assertTrue(check_published(config(), "v1.4.0", ASSETS, transport).allowed)
        self.assertIn(("assets", "acme/widget", "v1.4.0"), transport.calls)

    def test_release_missing_an_expected_asset_is_blocked_and_names_it(self):
        decision = check_published(config(), "v1.4.0", ASSETS, FakeTransport(assets=["widget-linux"]))
        self.assertEqual((decision.allowed, decision.reason), (False, "asset-missing: widget-windows.exe"))

    def test_release_assets_are_queried_with_the_configured_repo_and_tag(self):
        """Oracle row 4: the wrong repository or tag must go unnoticed, so the call is pinned."""
        transport = FakeTransport(assets=list(ASSETS))
        self.assertTrue(check_published(config(), "v2.0.0", ASSETS, transport).allowed)
        self.assertEqual(transport.calls, [("assets", "acme/widget", "v2.0.0")])

    def test_unconfigured_unresolvable_or_malformed_listing_is_blocked(self):
        for name, expected, transport in (("expected-assets-unconfigured", (), FakeTransport()),
                                          ("expected-assets-unconfigured", (1,), FakeTransport()),
                                          ("release-unresolved", ASSETS, FakeTransport(assets=OSError("down"))),
                                          ("release-assets-malformed", ASSETS, FakeTransport(assets=None)),
                                          ("release-assets-malformed", ASSETS, FakeTransport(assets=[1, 2]))):
            with self.subTest(name=name):
                self.assertEqual(check_published(config(), "v1.4.0", expected, transport).reason, name)


class VerifyTest(unittest.TestCase):
    def before(self, candidate=A, tag="v1.4.0", observed=None, transport=None):
        return verify_before(config(), candidate, tag, "1.4.0", BUILD, observed or dict(BUILD), transport or FakeTransport())

    def test_fully_valid_candidate_is_allowed_before_publication(self):
        self.assertEqual((self.before().allowed, self.before().reason), (True, "ok"))

    def test_each_failure_blocks_before_publication_with_its_reason(self):
        for name, kwargs in (("candidate-not-tip", {"candidate": B}), ("tag-version-mismatch", {"tag": "v2.0.0"}),
                             ("checksum-mismatch: widget-linux", {"observed": dict(BUILD, **{"widget-linux": D2})})):
            with self.subTest(name=name):
                self.assertEqual(self.before(**kwargs).reason, name)

    def test_the_first_failing_check_stops_the_run(self):
        transport = FakeTransport()
        self.before(candidate=B, transport=transport)
        self.assertEqual(len(transport.calls), 1)

    def test_fully_valid_release_is_allowed_after_publication(self):
        self.assertTrue(verify_after(config(), "v1.4.0", "1.4.0", ASSETS, FakeTransport()).allowed)

    def test_published_release_failures_block_after_publication(self):
        for name, tag, transport in (("tag-version-mismatch", "v1.4.1", FakeTransport()),
                                     ("asset-missing: widget-windows.exe", "v1.4.0", FakeTransport(assets=["widget-linux"]))):
            with self.subTest(name=name):
                self.assertEqual(verify_after(config(), tag, "1.4.0", ASSETS, transport).reason, name)


if __name__ == "__main__":
    unittest.main()
