"""Native verifier assignment must not degrade to an external harness."""
import json
import os
from pathlib import Path
import tempfile
import unittest

from scripts.reviewer_dispatch import DispatchError, dispatch, native_environment

HEAD = "a" * 40
BASE = "b" * 40


class NativeVerifier(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        (self.repo / "build").mkdir(parents=True)
        binary = self.repo / "build" / "flowstate"
        binary.write_text("fake executable", encoding="utf-8")
        binary.chmod(0o700)
        self.workspace = self.root / "evidence"
        self.workspace.mkdir(mode=0o700)
        self.packet = self.root / "review.md"
        self.packet.write_text("Verify the title validation and negative API oracle.")

    def call(self, invoker):
        return dispatch(self.repo, self.workspace, self.packet, 280,
                        HEAD, BASE, "author-session", "glm-5.3", invoke=invoker)

    def test_native_flowstate_is_the_only_spawned_binary(self):
        calls = []

        def fake(args, timeout):
            calls.append(args)
            if args[2] == "new":
                return json.dumps({"id": "verifier-session"})
            return '{"type":"session.completed"}\n'

        evidence = self.call(fake)
        self.assertEqual([Path(args[0]).name for args in calls], ["flowstate"] * 3)
        self.assertEqual([args[2] for args in calls], ["new", "run", "events"])
        self.assertNotIn("opencode", " ".join(" ".join(x) for x in calls).lower())
        self.assertFalse(evidence["review_accepted"])
        self.assertEqual(evidence["reviewer_session"], "verifier-session")

    def test_legacy_opencode_hints_are_not_forwarded(self):
        from unittest.mock import patch
        with patch.dict(os.environ, {"OPENCODE": "1", "OPENCODE_PROVIDER": "legacy",
                                     "AI_MODEL": "invented", "ZAI_API_KEY": "test-key"}):
            env = native_environment()
        self.assertNotIn("OPENCODE", env)
        self.assertNotIn("OPENCODE_PROVIDER", env)
        self.assertNotIn("AI_MODEL", env)
        self.assertEqual(env["ZAI_API_KEY"], "test-key")

    def test_absent_native_binary_fails_closed(self):
        (self.repo / "build" / "flowstate").unlink()
        with self.assertRaisesRegex(DispatchError, "no external fallback"):
            self.call(lambda *_: self.fail("must not spawn an alternative"))

    def test_self_review_is_rejected(self):
        with self.assertRaisesRegex(DispatchError, "must differ"):
            self.call(lambda args, timeout: json.dumps({"id": "author-session"}))

    def test_workspace_in_checkout_is_rejected(self):
        inside = self.repo / "evidence"
        inside.mkdir(mode=0o700)
        with self.assertRaisesRegex(DispatchError, "outside the checkout"):
            dispatch(self.repo, inside, self.packet, 280, HEAD, BASE,
                     "author-session", "glm-5.3", invoke=lambda *_: "")

    def test_invalid_candidate_is_rejected(self):
        with self.assertRaisesRegex(DispatchError, "exact head/base"):
            dispatch(self.repo, self.workspace, self.packet, 280, "bad", BASE,
                     "author-session", "glm-5.3", invoke=lambda *_: "")

    def test_native_failure_is_not_a_review_pass(self):
        def unavailable(args, timeout):
            if args[2] == "new":
                return '{"id":"verifier-session"}'
            raise DispatchError("provider failed")

        with self.assertRaisesRegex(DispatchError, "provider failed"):
            self.call(unavailable)
        self.assertFalse((self.workspace / "verifier-dispatch.json").exists())


if __name__ == "__main__":
    unittest.main()
