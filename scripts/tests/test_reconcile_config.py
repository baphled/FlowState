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


class LoadConfigTest(unittest.TestCase):
    def write(self, text):
        handle = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
        self.addCleanup(os.unlink, handle.name)
        handle.write(text)
        handle.close()
        return handle.name

    def test_loads_file(self):
        self.assertEqual(load_config(self.write(json.dumps(VALID))).repo, "acme/widget")

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


if __name__ == "__main__":
    unittest.main()
