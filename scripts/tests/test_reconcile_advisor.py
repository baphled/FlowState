import http.server
import json
import threading
import time
import unittest
from scripts.reconcile_advisor import advise

REASONS = ["NO_WATCHED_CHANGE"]
SAFE = {"affects": {"type": "noul", "noul": 0.05}, "judgement": {"type": "choice", "choice": "safe"}}
UNSURE_NOUL = {"affects": {"type": "noul", "noul": 0.5}, "judgement": {"type": "choice", "choice": "safe"}}
UNSAFE_CHOICE = {"affects": {"type": "noul", "noul": 0.05}, "judgement": {"type": "choice", "choice": "unsafe"}}


def answer(answers):
    return json.dumps({"model": "m", "answers": answers, "usage": {"output_tokens": 0}}).encode()


class StubServer(http.server.ThreadingHTTPServer):
    """A loopback System One stand-in on an ephemeral port that records each request it receives."""
    daemon_threads = True

    def __init__(self, status=200, body=b"", headers=None, delay=0):
        super().__init__(("127.0.0.1", 0), StubHandler)
        self.status, self.body, self.headers, self.delay, self.seen = status, body, headers or {}, delay, []

    @property
    def url(self):
        return f"http://127.0.0.1:{self.server_address[1]}"


class StubHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):  # reached only when a client follows a redirect, which the advisor must never do
        self.reply(None)

    def do_POST(self):
        self.reply(json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0)))))

    def reply(self, request):
        self.server.seen.append((self.command, self.path, request))
        time.sleep(self.server.delay)
        self.send_response(self.server.status)
        for name, value in self.server.headers.items():
            self.send_header(name, value)
        self.send_header("Content-Length", str(len(self.server.body)))
        self.end_headers()
        try:
            self.wfile.write(self.server.body)
        except OSError:  # the client gave up first (timeout or an oversize refusal); nothing to deliver
            pass

    def log_message(self, *args):
        pass


class AdvisorCase(unittest.TestCase):
    def stub(self, **kwargs):
        server = StubServer(**kwargs)
        threading.Thread(target=server.serve_forever, args=(0.01,), daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        return server

    def advise(self, outcome, server, **kwargs):
        kwargs.setdefault("base_url", server.url)
        return advise(outcome, list(REASONS), "kev:test", **kwargs)


class TestEscalation(AdvisorCase):
    def test_non_material_escalates_to_indeterminate_on_each_unsafe_answer(self):
        for answers in (UNSURE_NOUL, UNSAFE_CHOICE):
            with self.subTest(answers=answers):
                outcome, reasons = self.advise("non_material", self.stub(body=answer(answers)))
                self.assertEqual((outcome, reasons), ("indeterminate", REASONS + ["ADVISOR_ESCALATED"]))

    def test_a_safe_answer_leaves_non_material_and_reasons_unchanged(self):
        self.assertEqual(self.advise("non_material", self.stub(body=answer(SAFE))), ("non_material", REASONS))

    def test_indeterminate_stays_indeterminate_whatever_the_advice(self):
        for answers in (SAFE, UNSAFE_CHOICE):
            with self.subTest(answers=answers):
                outcome, reasons = self.advise("indeterminate", self.stub(body=answer(answers)))
                self.assertEqual(outcome, "indeterminate")

    def test_other_outcomes_are_never_queried_or_changed(self):
        for outcome in ("material", "current", "block", "allow", "pass", "bogus", None):
            with self.subTest(outcome=outcome):
                server = self.stub(body=answer(SAFE))
                self.assertEqual(self.advise(outcome, server), (outcome, REASONS))
                self.assertEqual(server.seen, [])

    def test_a_safe_verdict_never_turns_material_into_non_material(self):
        server = self.stub(body=answer(SAFE))
        self.assertEqual(self.advise("material", server)[0], "material")


class TestRequest(AdvisorCase):
    def test_posts_model_state_and_typed_questions_to_the_system_one_path(self):
        server = self.stub(body=answer(SAFE))
        self.advise("non_material", server)
        method, path, body = server.seen[0]
        self.assertEqual((method, path, body["model"]), ("POST", "/v1/systemone", "kev:test"))
        self.assertEqual({k: v["type"] for k, v in body["questions"].items()},
                         {"affects": "noul", "judgement": "choice"})

    def test_state_carries_only_the_outcome_and_reason_codes(self):
        server = self.stub(body=answer(SAFE))
        self.advise("non_material", server)
        self.assertEqual(server.seen[0][2]["state"], "outcome: non_material\nreasons: NO_WATCHED_CHANGE")


class TestNoAdvice(AdvisorCase):
    def test_unset_model_returns_no_advice_and_makes_no_request(self):
        for model in ("", None, 7):
            with self.subTest(model=model):
                server = self.stub(body=answer(UNSAFE_CHOICE))
                self.assertEqual(advise("non_material", list(REASONS), model, base_url=server.url),
                                 ("non_material", REASONS))
                self.assertEqual(server.seen, [])

    def test_non_loopback_or_unsafe_base_url_is_refused_without_a_request(self):
        server = self.stub(body=answer(UNSAFE_CHOICE))
        port = server.server_address[1]
        for url in (f"http://127.0.0.2:{port}", f"http://localhost.evil.test:{port}", f"http://[::1]:{port}",
                    f"https://127.0.0.1:{port}", f"http://user@127.0.0.1:{port}", f"http://127.0.0.1:{port}/x",
                    f"http://127.0.0.1:{port}?a=1", "http://example.com", "ftp://127.0.0.1", "", None, 5):
            with self.subTest(url=url):
                self.assertEqual(self.advise("non_material", server, base_url=url), ("non_material", REASONS))
        self.assertEqual(server.seen, [])

    def test_localhost_is_accepted_and_dialled_as_loopback(self):
        server = self.stub(body=answer(UNSAFE_CHOICE))
        url = f"http://localhost:{server.server_address[1]}"
        self.assertEqual(self.advise("non_material", server, base_url=url)[0], "indeterminate")

    def test_redirect_is_not_followed(self):
        for status in (301, 302, 303, 307, 308):
            with self.subTest(status=status):
                target = self.stub(body=answer(UNSAFE_CHOICE))
                server = self.stub(status=status, headers={"Location": target.url + "/v1/systemone"})
                self.assertEqual(self.advise("non_material", server), ("non_material", REASONS))
                self.assertEqual(target.seen, [])

    def test_unreachable_endpoint_leaves_the_outcome_unchanged(self):
        server = self.stub()
        url = server.url
        server.shutdown()
        server.server_close()
        self.assertEqual(self.advise("non_material", server, base_url=url), ("non_material", REASONS))

    def test_timeout_leaves_the_outcome_unchanged(self):
        server = self.stub(body=answer(UNSAFE_CHOICE), delay=1.0)
        started = time.monotonic()
        self.assertEqual(self.advise("non_material", server, timeout=0.2), ("non_material", REASONS))
        self.assertLess(time.monotonic() - started, 0.9)

    def test_invalid_timeout_is_refused_without_a_request(self):
        for timeout in (0, -1, float("nan"), "5", True, None):
            with self.subTest(timeout=timeout):
                server = self.stub(body=answer(UNSAFE_CHOICE))
                self.assertEqual(self.advise("non_material", server, timeout=timeout), ("non_material", REASONS))
                self.assertEqual(server.seen, [])

    def test_timeout_above_the_cap_is_clamped_to_thirty_seconds(self):
        from unittest import mock
        with mock.patch("scripts.reconcile_advisor.OPENER.open", side_effect=OSError) as opened:
            advise("non_material", list(REASONS), "kev:test", timeout=300)
        self.assertEqual(opened.call_args.kwargs["timeout"], 30)

    def test_malformed_answers_return_no_advice(self):
        bad = [b"", b"not json", b"[]", b'{"answers": []}', b'{"answers": {}}', b"\xff\xfe",
               answer({"affects": SAFE["affects"]}),
               answer({**UNSAFE_CHOICE, "judgement": {"type": "choice", "choice": "other"}}),
               answer({**UNSAFE_CHOICE, "affects": {"type": "noul", "noul": "high"}}),
               answer({**UNSAFE_CHOICE, "affects": {"type": "noul", "noul": 1.5}}),
               answer({**UNSAFE_CHOICE, "affects": {"type": "noul", "noul": True}}),
               answer({**UNSAFE_CHOICE, "affects": {"type": "choice", "noul": 0.9}}),
               b'{"answers": {"affects": {"type": "noul", "noul": NaN}, "judgement": {"type": "choice", "choice": "unsafe"}}}',
               b"x" * 70000]
        for body in bad:
            with self.subTest(body=body[:40]):
                self.assertEqual(self.advise("non_material", self.stub(body=body)), ("non_material", REASONS))

    def test_a_non_success_status_returns_no_advice(self):
        for status in (400, 500):
            with self.subTest(status=status):
                server = self.stub(status=status, body=answer(UNSAFE_CHOICE))
                self.assertEqual(self.advise("non_material", server), ("non_material", REASONS))

    def test_malformed_reasons_return_no_advice_and_no_request(self):
        for reasons in (None, "NO_WATCHED_CHANGE", ["lower case"], [3], ["A"] * 33):
            with self.subTest(reasons=reasons):
                server = self.stub(body=answer(UNSAFE_CHOICE))
                self.assertEqual(advise("non_material", reasons, "kev:test", base_url=server.url),
                                 ("non_material", reasons))
                self.assertEqual(server.seen, [])


if __name__ == "__main__":
    unittest.main()
