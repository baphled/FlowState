"""Optional System One advice on a drift outcome; standalone, and imported by no gate module.

`advise(outcome, reasons, model, base_url=DEFAULT_URL, timeout=DEFAULT_TIMEOUT)` returns `(outcome, reasons)`. For an
`indeterminate` or `non_material` outcome it asks the loopback System One endpoint (ADR-012: `POST /v1/systemone` with
a `noul` and a `choice` question) whether the change might matter. The only change it can make is `non_material` to
`indeterminate`, with `ADVISOR_ESCALATED` appended to the reasons. Every other outcome is returned untouched and is
never sent to the model, so advice never turns a block into an allow, an indeterminate into a pass, or a material
outcome into a non-material one.

No advice, meaning the input comes back unchanged, results from: an unset model (`reconcile.json` `system_one_model`),
a malformed outcome, reason list or timeout, a base URL that is not plain `http` to `localhost` or `127.0.0.1` with no
userinfo, path, query or fragment, a redirect, a non-200 status, an unreachable endpoint, a timeout, a body over 64 KiB
and any answer that is not the two expected, well-typed answers. The request is dialled at `127.0.0.1` even for
`localhost`, proxies are ignored, and the state sent holds only the outcome and the upper-case reason codes.
"""
import http.client
import json
import re
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import ProxyHandler, Request, build_opener
from scripts.admission_transport import RejectRedirects

DEFAULT_URL, DEFAULT_TIMEOUT, MAX_TIMEOUT = "http://localhost:11434", 10, 30
ELIGIBLE, LOOPBACK, MAX_BODY, MAX_REASONS = ("indeterminate", "non_material"), ("localhost", "127.0.0.1"), 65536, 32
NOUL_ESCALATES_AT, CHOICES = 0.4, ("safe", "unsafe", "unsure")
OPENER = build_opener(ProxyHandler({}), RejectRedirects())
QUESTIONS = {
    "affects": {"type": "noul", "instructions": "Could this upstream change affect the packet's watched paths?"},
    "judgement": {"type": "choice", "instructions": "Is leaving this drift outcome as it is safe?",
                  "criteria": {name: name for name in CHOICES}},
}


def _dial_target(base_url):
    """`(dial URL, Host header)` for a plain loopback base URL, or None for anything else."""
    if not isinstance(base_url, str):
        return None
    parts = urlsplit(base_url)
    try:
        port = parts.port or 80
    except ValueError:
        return None
    plain = (parts.scheme, "@" in parts.netloc, parts.path in ("", "/"), parts.query, parts.fragment)
    if plain != ("http", False, True, "", "") or parts.hostname not in LOOPBACK:
        return None
    return f"http://127.0.0.1:{port}/v1/systemone", parts.netloc


def _valid_input(outcome, reasons, model, timeout):
    reason_list = isinstance(reasons, list) and len(reasons) <= MAX_REASONS
    codes = reason_list and all(isinstance(r, str) and re.fullmatch(r"[A-Z][A-Z0-9_]*", r) for r in reasons)
    number = isinstance(timeout, (int, float)) and not isinstance(timeout, bool) and timeout > 0
    return outcome in ELIGIBLE and bool(codes) and number and isinstance(model, str) and model != ""


def _post(target, model, state, timeout):
    """The decoded JSON answer, or None for any transport, status, size or decoding failure."""
    body = json.dumps({"model": model, "state": state, "questions": QUESTIONS}).encode()
    request = Request(target[0], data=body, method="POST",
                      headers={"Content-Type": "application/json", "Host": target[1]})
    try:
        with OPENER.open(request, timeout=min(timeout, MAX_TIMEOUT)) as response:
            raw = response.read(MAX_BODY + 1) if response.status == 200 else b""
        return json.loads(raw) if 0 < len(raw) <= MAX_BODY else None
    except HTTPError as error:  # a refused redirect or an error status; release its socket
        error.close()
        return None
    except (OSError, URLError, http.client.HTTPException, ValueError, RecursionError):
        return None


def _verdict(reply):
    """True to escalate, False for a safe answer, None when the reply is not the two expected answers."""
    answers = reply.get("answers") if isinstance(reply, dict) else None
    noul = answers.get("affects") if isinstance(answers, dict) else None
    pick = answers.get("judgement") if isinstance(answers, dict) else None
    if not (isinstance(noul, dict) and isinstance(pick, dict)):
        return None
    probability, choice = noul.get("noul"), pick.get("choice")
    typed = (noul.get("type"), pick.get("type")) == ("noul", "choice")
    number = isinstance(probability, (int, float)) and not isinstance(probability, bool)
    if not (typed and number and 0 <= probability <= 1 and choice in CHOICES):
        return None
    return probability >= NOUL_ESCALATES_AT or choice != "safe"


def advise(outcome, reasons, model, base_url=DEFAULT_URL, timeout=DEFAULT_TIMEOUT):
    """Return `(outcome, reasons)`, escalated to indeterminate only on a valid unsafe answer; see the module text."""
    target = _dial_target(base_url)
    if target is None or not _valid_input(outcome, reasons, model, timeout):
        return outcome, reasons
    state = f"outcome: {outcome}\nreasons: {','.join(reasons)}"
    reply = _post(target, model, state, timeout)
    if reply is None or _verdict(reply) is not True:
        return outcome, reasons
    return "indeterminate", [*reasons, "ADVISOR_ESCALATED"]
