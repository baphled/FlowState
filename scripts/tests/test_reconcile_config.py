import copy
import json
import os
import tempfile
import unittest
from scripts.reconcile_config import ReconcileError, load_config, parse_config

VALID = {
    "repo": "acme/widget",
    "remote": {"name": "origin", "url": "https://example.test/acme/widget.git"},
    "integration_branch": "dev",
    "release_branch": "stable",
    "resolve_modes": {"hook": ["ls-remote", "api"], "ci": ["ls-remote", "api"],
                      "runner": ["api"], "merge_script": ["api"]},
}


SENSITIVE = [".github/workflows/release.yml", "go.mod", "go.sum",
             "scripts/admission_service.py", "scripts/requirements-admission.txt"]
GLOBS = [".github/**", ".githooks/**", "rules/**", "Makefile", "go.mod", ".env.ci", "reconcile.json",
         "scripts/base_checkers.py", "scripts/reconcile_*.py", "scripts/admission_*.py",
         "scripts/review_*.py", "scripts/status_issuer.py", "scripts/requirements-admission.txt",
         "scripts/tests/test_reconcile_*.py", "scripts/tests/test_admission_*.py",
         "scripts/tests/test_base_checkers.py"]
GATE_KEYS = {"plan_approval_public_key": "", "activation_ref": "", "system_one_model": "",
             "release_sensitive_paths": SENSITIVE, "protected_globs": GLOBS}
FULL = dict(VALID, **GATE_KEYS)
KEY = "0123456789abcdef" * 4
REF = "dropbox:/FlowState/Metadata/attempts/2026-11-26-sdlc-activation/activation.json"


def variant(**changes):
    data = copy.deepcopy(VALID)
    data.update(changes)
    return data


class ParseConfigTest(unittest.TestCase):
    def test_valid_config_applies_defaults(self):
        config = parse_config(copy.deepcopy(VALID))
        self.assertEqual((config.ttl_seconds, config.retry, config.protected_globs), (900, 1, ()))

    def test_valid_config_exposes_values(self):
        config = parse_config(copy.deepcopy(VALID))
        self.assertEqual((config.repo, config.remote_name, config.integration_branch, config.release_branch),
                         ("acme/widget", "origin", "dev", "stable"))

    def test_overrides_are_kept(self):
        config = parse_config(variant(ttl_seconds=5, retry=0, protected_globs=["a/**"]))
        self.assertEqual((config.ttl_seconds, config.retry, config.protected_globs), (5, 0, ("a/**",)))

    def test_every_missing_top_level_key_raises(self):
        for key in VALID:
            data = copy.deepcopy(VALID)
            del data[key]
            with self.subTest(key=key), self.assertRaises(ReconcileError):
                parse_config(data)

    def test_unknown_key_raises(self):
        with self.assertRaises(ReconcileError):
            parse_config(variant(extra=1))

    def test_unknown_remote_key_raises(self):
        with self.assertRaises(ReconcileError):
            parse_config(variant(remote={"name": "o", "url": "u", "x": 1}))

    def test_missing_context_raises(self):
        modes = {k: v for k, v in VALID["resolve_modes"].items() if k != "runner"}
        with self.assertRaises(ReconcileError):
            parse_config(variant(resolve_modes=modes))

    def test_unknown_context_raises(self):
        with self.assertRaises(ReconcileError):
            parse_config(variant(resolve_modes=dict(VALID["resolve_modes"], other=["api"])))

    def test_bad_modes_raise(self):
        for bad in ([], ["git"], "api", ["api", "api"], [1]):
            modes = dict(VALID["resolve_modes"], hook=bad)
            with self.subTest(bad=bad), self.assertRaises(ReconcileError):
                parse_config(variant(resolve_modes=modes))

    def test_bad_scalars_raise(self):
        for key, bad in (("repo", "noslash"), ("repo", "a/b c"), ("repo", "."), ("repo", ".."),
                         ("repo", "../x"), ("repo", "x/.."), ("repo", "a/b\n"),
                         ("integration_branch", "../../../user"), ("integration_branch", "x?y#z"),
                         ("integration_branch", "-x"), ("release_branch", "a..b"), ("integration_branch", ""), ("ttl_seconds", 0),
                         ("ttl_seconds", True), ("retry", -1), ("protected_globs", "x"),
                         ("protected_globs", [""])):
            with self.subTest(key=key, bad=bad), self.assertRaises(ReconcileError):
                parse_config(variant(**{key: bad}))

    def test_unsafe_remote_urls_raise(self):
        for bad in ("origin", "-oupload-pack=x", "ext::sh -c id", "--x", "relative/path", "https://", "a b"):
            with self.subTest(bad=bad), self.assertRaises(ReconcileError):
                parse_config(variant(remote={"name": "origin", "url": bad}))

    def test_real_remote_urls_are_accepted(self):
        for good in ("https://h.test/a/b.git", "ssh://git@h.test/a/b", "git@h.test:a/b.git", "/srv/a/b.git"):
            with self.subTest(good=good):
                self.assertEqual(parse_config(variant(remote={"name": "origin", "url": good})).remote_url, good)

    def test_non_object_raises(self):
        with self.assertRaises(ReconcileError):
            parse_config([])


class GateKeysTest(unittest.TestCase):
    def test_unset_defaults_are_empty(self):
        config = parse_config(copy.deepcopy(VALID))
        self.assertEqual((config.plan_approval_public_key, config.activation_ref, config.system_one_model,
                          config.release_sensitive_paths), ("", "", "", ()))

    def test_values_are_exposed(self):
        config = parse_config(variant(plan_approval_public_key=KEY, activation_ref=REF,
                                      system_one_model="router/fast", release_sensitive_paths=SENSITIVE))
        self.assertEqual((config.plan_approval_public_key, config.activation_ref, config.system_one_model,
                          config.release_sensitive_paths), (KEY, REF, "router/fast", tuple(SENSITIVE)))

    def test_bad_public_keys_raise(self):
        for bad in (KEY[:-1], KEY + "0", KEY.upper(), "g" * 64, KEY + "\n", " " + KEY, 5, None):
            with self.subTest(bad=bad), self.assertRaises(ReconcileError):
                parse_config(variant(plan_approval_public_key=bad))

    def test_bad_activation_refs_raise(self):
        base = "dropbox:/FlowState/Metadata/attempts/"
        for bad in ("activation.json", base + "a/b/c.json", base + "a/c.txt", base + "../c.json",
                    base + "a/.json", "dropbox:/FlowState/Dev/a/c.json", "file:/FlowState/Metadata/attempts/a/c.json",
                    base + "a/c.json\n", base + "a b/c.json", base + "./c.json", base + "a/../c.json", 7):
            with self.subTest(bad=bad), self.assertRaises(ReconcileError):
                parse_config(variant(activation_ref=bad))

    def test_bad_system_one_models_raise(self):
        for bad in (" ", "a b", "-x", "a\n", 3, ["m"]):
            with self.subTest(bad=bad), self.assertRaises(ReconcileError):
                parse_config(variant(system_one_model=bad))

    def test_non_literal_sensitive_paths_raise(self):
        for bad in ("scripts/*.py", "a/**", "go.m?d", "[a]/b", "{a,b}", "/etc/passwd", "../go.mod", "a/../b",
                    "a//b", "a/", "", ".", "./go.mod", "a b", "go.mod\n", "a\\b", "-x", "~/x", "$HOME/x", 4):
            with self.subTest(bad=bad), self.assertRaises(ReconcileError):
                parse_config(variant(release_sensitive_paths=[bad]))

    def test_sensitive_paths_must_be_a_unique_list(self):
        for bad in ("go.mod", ["go.mod", "go.mod"], None):
            with self.subTest(bad=bad), self.assertRaises(ReconcileError):
                parse_config(variant(release_sensitive_paths=bad))


class LoadConfigTest(unittest.TestCase):
    def write(self, text):
        handle = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
        self.addCleanup(os.unlink, handle.name)
        handle.write(text)
        handle.close()
        return handle.name

    def test_loads_file(self):
        self.assertEqual(load_config(self.write(json.dumps(FULL))).repo, "acme/widget")

    def test_loaded_file_carries_gate_keys(self):
        config = load_config(self.write(json.dumps(FULL)))
        self.assertEqual((config.release_sensitive_paths, config.protected_globs),
                         (tuple(SENSITIVE), tuple(GLOBS)))

    def test_file_missing_any_gate_key_raises(self):
        for key in GATE_KEYS:
            data = {k: v for k, v in FULL.items() if k != key}
            with self.subTest(key=key), self.assertRaises(ReconcileError):
                load_config(self.write(json.dumps(data)))

    def test_file_with_unknown_key_raises(self):
        with self.assertRaises(ReconcileError):
            load_config(self.write(json.dumps(dict(FULL, extra=1))))

    def test_file_with_wildcard_sensitive_path_raises(self):
        with self.assertRaises(ReconcileError):
            load_config(self.write(json.dumps(dict(FULL, release_sensitive_paths=["scripts/*.py"]))))

    def test_file_with_short_public_key_raises(self):
        with self.assertRaises(ReconcileError):
            load_config(self.write(json.dumps(dict(FULL, plan_approval_public_key="abc"))))

    def test_invalid_json_raises(self):
        with self.assertRaises(ReconcileError):
            load_config(self.write("{"))

    def test_missing_file_raises(self):
        with self.assertRaises(ReconcileError):
            load_config("/nonexistent/reconcile.json")

    def test_repository_config_is_valid(self):
        root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
        config = load_config(os.path.join(root, "reconcile.json"))
        self.assertEqual(config.resolve_modes["runner"], ("api",))

    def test_repository_config_gate_keys_are_fixed(self):
        root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
        config = load_config(os.path.join(root, "reconcile.json"))
        self.assertEqual((config.plan_approval_public_key, config.activation_ref, config.system_one_model,
                          config.release_sensitive_paths, config.protected_globs),
                         ("6fb1f441896777336a174db186520ca585e29d95968851c708a19c667a2f8f15", "", "",
                          tuple(SENSITIVE), tuple(GLOBS)))


if __name__ == "__main__":
    unittest.main()
