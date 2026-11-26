import copy
import http.server
import os
import subprocess
import tempfile
import threading
import unittest
from unittest import mock
from urllib.error import HTTPError
from scripts.reconcile_config import ReconcileError, parse_config
from scripts.reconcile_target import LiveTransport, resolve, resolve_twice

A, B = "a" * 40, "b" * 40
BASE = {
    "repo": "acme/widget",
    "remote": {"name": "origin", "url": "https://example.test/acme/widget.git"},
    "integration_branch": "dev",
    "release_branch": "stable",
    "resolve_modes": {"hook": ["ls-remote", "api"], "ci": ["ls-remote", "api"],
                      "runner": ["api"], "merge_script": ["api"]},
}


def config():
    return parse_config(copy.deepcopy(BASE))


class FakeTransport:
    def __init__(self, ls=A, api=A, identity="acme/widget"):
        self.ls, self.api_sha, self.identity = ls, api, identity

    def _value(self, value):
        if isinstance(value, Exception):
            raise value
        return value.pop(0) if isinstance(value, list) else value

    def ls_remote(self, url, ref):
        return self._value(self.ls)

    def api(self, repo, branch):
        return self.identity, self._value(self.api_sha)


class ResolveTest(unittest.TestCase):
    def test_both_sources_match_returns_sha_and_recorded_sources(self):
        target = resolve(config(), "hook", FakeTransport())
        self.assertEqual((target.sha, target.sources), (A, ("ls-remote", "api")))

    def test_unreachable_source_or_wrong_identity_or_disagreement_raises(self):
        for context, transport in (("hook", FakeTransport(ls=OSError("down"))),
                                   ("ci", FakeTransport(api=OSError("down"))),
                                   ("runner", FakeTransport(identity="evil/widget")),
                                   ("hook", FakeTransport(ls=A, api=B))):
            with self.subTest(context=context), self.assertRaises(ReconcileError):
                resolve(config(), context, transport)

    def test_api_only_with_reachable_api_returns_sha(self):
        self.assertEqual(resolve(config(), "runner", FakeTransport(ls=OSError("unused"))).sha, A)

    def test_malformed_sha_raises(self):
        for bad in ("abc", "a" * 4, "a" * 7, "a" * 39, "A" * 40, "g" * 40, "a" * 41, None, ""):
            with self.subTest(bad=bad), self.assertRaises(ReconcileError):
                resolve(config(), "runner", FakeTransport(api=bad))


class ResolveTwiceTest(unittest.TestCase):
    def test_changed_sha_raises(self):
        with self.assertRaises(ReconcileError):
            resolve_twice(config(), "runner", FakeTransport(api=[A, B]))


def git(*args, cwd=None):
    ident = {"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t",
             "GIT_COMMITTER_EMAIL": "t@t"}
    return subprocess.run(("git",) + args, cwd=cwd, env=dict(os.environ, **ident), check=True,
                          capture_output=True, text=True).stdout.strip()


def make_remote(root, name, message):
    bare, work = os.path.join(root, name + ".git"), os.path.join(root, name)
    git("init", "--bare", bare)
    git("init", work)
    git("commit", "--allow-empty", "-m", message, cwd=work)
    git("push", bare, "HEAD:refs/heads/dev", cwd=work)
    return git("rev-parse", "HEAD", cwd=work), "file://" + bare


class LiveLsRemoteTest(unittest.TestCase):
    def setUp(self):
        self.root = self.enterContext(tempfile.TemporaryDirectory())
        self.sha, self.url = make_remote(self.root, "bare", "x")

    def checkout(self):
        path = os.path.join(self.root, "checkout")
        git("init", path)
        self.addCleanup(os.chdir, os.getcwd())
        os.chdir(path)
        return path

    def test_live_ls_remote_returns_branch_sha_or_raises_for_missing_ref(self):
        self.assertEqual(LiveTransport().ls_remote(self.url, "refs/heads/dev"), self.sha)
        self.assertRaises(ReconcileError, LiveTransport().ls_remote, self.url, "refs/heads/absent")

    def test_checkout_insteadof_does_not_rewrite_url(self):
        _, other = make_remote(self.root, "other", "other")
        git("config", "url." + other + ".insteadOf", self.url, cwd=(checkout := self.checkout()))
        with mock.patch.object(tempfile, "tempdir", checkout):
            self.assertEqual(LiveTransport().ls_remote(self.url, "refs/heads/dev"), self.sha)

    def test_inherited_git_config_env_does_not_rewrite_url(self):
        _, other = make_remote(self.root, "other", "other")
        env = {"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "url." + other + ".insteadOf",
               "GIT_CONFIG_VALUE_0": self.url}
        with mock.patch.dict(os.environ, env):
            self.assertEqual(LiveTransport().ls_remote(self.url, "refs/heads/dev"), self.sha)

    def test_remote_name_and_option_like_urls_are_refused(self):
        self.checkout()
        for bad in ("origin", "-oupload-pack=x", "ext::sh -c id"):
            with self.subTest(bad=bad), self.assertRaises(ReconcileError):
                LiveTransport().ls_remote(bad, "refs/heads/dev")


class LsRemoteInvocationTest(unittest.TestCase):
    def setUp(self):
        done = subprocess.CompletedProcess([], 0, stdout=A + "\trefs/heads/dev\n")
        self.run = self.enterContext(mock.patch("scripts.reconcile_target.subprocess.run", return_value=done))
        env = {"GIT_DIR": "/x", "GIT_SSH_COMMAND": "evil", "HOME": "/home/u", "SSH_AUTH_SOCK": "/s", "GITHUB_TOKEN": "x", "GH_TOKEN": "x"}
        with mock.patch.dict(os.environ, env):
            LiveTransport().ls_remote("https://h.test/a/b", "refs/heads/dev")
        self.args, self.kwargs = self.run.call_args

    def test_row_for_a_different_ref_raises(self):
        self.run.return_value.stdout = A + "\trefs/heads/other\n"
        self.assertRaises(ReconcileError, LiveTransport().ls_remote, "https://h.test/a/b", "refs/heads/dev")

    def test_command_and_options(self):
        self.assertEqual((self.args[0], self.kwargs["timeout"], self.kwargs["check"]),
                         (["git", "ls-remote", "--", "https://h.test/a/b", "refs/heads/dev"], 30, True))

    def test_env_is_scrubbed_pinned_and_cwd_is_not_checkout(self):
        env = self.kwargs["env"]
        self.assertEqual((sorted(k for k in env if k.startswith("GIT_")), env["GIT_CONFIG_GLOBAL"], env["GIT_CONFIG_SYSTEM"], env["GIT_TERMINAL_PROMPT"],
                          env["HOME"], env["SSH_AUTH_SOCK"], sorted(k for k in env if k in ("GITHUB_TOKEN", "GH_TOKEN"))),
                         (["GIT_CEILING_DIRECTORIES", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM",
                           "GIT_CONFIG_SYSTEM", "GIT_TERMINAL_PROMPT"], "/dev/null", "/dev/null", "0", "/home/u", "/s", []))
        self.assertNotEqual(os.path.realpath(self.kwargs["cwd"]), os.path.realpath(os.getcwd()))


class FakeResponse:
    def __init__(self, body, status=200):
        self.body, self.status = body, status

    def read(self, size=-1):
        return self.body if size < 0 else self.body[:size]

    __enter__ = lambda self: self
    __exit__ = lambda self, *exc: False


class ApiTest(unittest.TestCase):
    TOKEN = "s3cret-token"
    ROOT = "https://api.github.com/repos/acme/widget"

    def call(self, routes, branch="dev", token=TOKEN, resolver=False):
        self.requests = []

        def opener(request):
            self.requests.append(request)
            reply = routes[request.full_url]
            if isinstance(reply, Exception):
                raise reply
            return reply if isinstance(reply, FakeResponse) else FakeResponse(reply)

        with mock.patch.dict(os.environ, {"GITHUB_TOKEN": token} if token else {}, clear=True), \
                mock.patch("scripts.reconcile_target.open_no_redirect", opener):
            if resolver:
                return resolve(config(), "runner", LiveTransport(), branch=branch)
            return LiveTransport().api("acme/widget", branch)

    def good(self, branch="dev", **changes):
        routes = {self.ROOT: b'{"full_name": "acme/widget"}',
                  self.ROOT + "/branches/" + branch: ('{"commit": {"sha": "%s"}}' % A).encode()}
        return dict(routes, **{self.ROOT + k: v for k, v in changes.items()})

    def test_returns_identity_and_sha_using_get_with_bearer_token(self):
        self.assertEqual(self.call(self.good()), ("acme/widget", A))
        self.assertEqual([(r.get_method(), r.get_header("Authorization")) for r in self.requests],
                         [("GET", "Bearer " + self.TOKEN)] * 2)

    def test_missing_token_raises_without_requests(self):
        with self.assertRaises(ReconcileError):
            self.call(self.good(), token=None)
        self.assertEqual(self.requests, [])

    def test_branch_is_quoted_into_url(self):
        self.call(self.good("feat%2Fx"), branch="feat/x")
        self.assertEqual(self.requests[1].full_url, self.ROOT + "/branches/feat%2Fx")

    def test_hostile_branches_raise_without_requests(self):
        for bad in ("", "../../../user", "?x#y", "-x", "a..b", "a b"):
            for resolver in (False, True):
                with self.subTest(bad=bad, resolver=resolver), self.assertRaises(ReconcileError):
                    self.call(self.good(), branch=bad, resolver=resolver)
                self.assertEqual(self.requests, [])

    def test_oversized_body_raises(self):
        with self.assertRaisesRegex(Exception, "8MiB"):
            self.call(self.good(**{"": b'{"full_name": "' + b"a" * 8388608 + b'"}'}))

    def test_duplicate_keys_raise(self):
        with self.assertRaisesRegex(Exception, "duplicate JSON object key"):
            self.call(self.good(**{"": b'{"full_name": "evil/x", "full_name": "acme/widget"}'}))

    def test_non_200_raises(self):
        with self.assertRaises(ReconcileError):
            self.call(self.good(**{"": FakeResponse(b"{}", status=204)}))

    def test_failures_neither_leak_the_token_nor_chain_the_cause(self):
        for failure in (HTTPError(self.ROOT, 401, "denied", {}, None), RuntimeError(self.TOKEN)):
            with self.subTest(failure=failure), self.assertRaises(ReconcileError) as caught:
                self.call(self.good(**{"": failure}), resolver=True)
            error = caught.exception
            self.assertNotIn(self.TOKEN, str(error) + repr(error))
            self.assertEqual((error.__cause__, error.__suppress_context__), (None, True))

    def test_branch_override_is_used_and_validated(self):
        self.assertEqual(resolve(config(), "hook", FakeTransport(), branch="rel").ref, "refs/heads/rel")
        self.assertRaises(ReconcileError, resolve, config(), "hook", FakeTransport(), branch="a/../b")


class RedirectTest(unittest.TestCase):
    def test_redirects_are_refused_and_not_followed(self):
        seen = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                seen.append((self.path, self.headers.get("Authorization")))
                self.send_response(302 if self.path == "/start" else 200)
                self.send_header("Location", "/stolen")
                self.end_headers()

            log_message = lambda *args: None

        server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        url = "http://127.0.0.1:%d/start" % server.server_port
        with self.assertRaises(HTTPError):
            LiveTransport()._get(url, "tok")
        self.assertEqual(seen, [("/start", "Bearer tok")])


if __name__ == "__main__":
    unittest.main()
