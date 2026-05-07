#!/usr/bin/env python3
"""Quorum gate for the Board Room swarm.

Reads a JSON request on stdin, validates that all five analyst
positions are present and that the bull and bear analysts reach
divergent decisions, and writes a JSON response on stdout.

Composed payload shape (see internal/app/gates/quorum-gate/manifest.yml):

    {"bull":      <bull-analyst output>,
     "bear":      <bear-analyst output>,
     "market":    <market-analyst output>,
     "financial": <financial-analyst output>,
     "technical": <technical-analyst output>}

Each value is the analyst's raw coord-store output, embedded by the
host's composeMultiKeyPayload as a JSON object when the upstream
analyst emitted JSON, or as a JSON string otherwise. The Board Room
contract requires every analyst to write a JSON object whose
`decision` field is one of "buy" / "sell" / "hold" / "invest" / "pass" /
"conditional"; the gate's divergence check looks at bull.decision vs
bear.decision case-insensitively after stripping whitespace.

Response shape:

    pass=True  -> {"pass": true}
    pass=False -> {"pass": false, "reason": "..."}

The ExtGateResponse decoder on the host side reads `pass` + `reason`
verbatim. A halt response includes a specific diagnostic so operators
can see at a glance which contract failed (missing analyst by name,
or collapsed adversarial review with the converged decision quoted).
"""
import base64
import binascii
import json
import sys


REQUIRED_ANALYSTS = ["bull", "bear", "market", "financial", "technical"]


def decode_payload(raw):
    """Coerce the request's `payload` field into a Python dict.

    Go's `encoding/json` marshals `[]byte` as a base64 string, so the
    host side's swarm.ExtGateRequest{Payload: <bytes>} arrives here as
    a base64-encoded value. Older example dispatchers (and direct unit
    tests) feed a raw JSON string or a pre-parsed object instead. Try
    each in turn so the gate is portable across both call shapes —
    matches the relevance-gate's decode_payload helper introduced in
    commit `885f44aa`.
    """
    if raw is None or raw == "":
        return {}
    if isinstance(raw, dict):
        return raw
    if isinstance(raw, str):
        # Try direct JSON first — covers tests / hand-rolled callers
        # that send a JSON string verbatim.
        try:
            return json.loads(raw)
        except Exception:
            pass
        # Fall back to base64 + JSON, which is the runtime path the
        # subprocessRunner takes when ExtGateRequest.Payload is a
        # []byte slice (Go marshals []byte as base64).
        try:
            decoded = base64.b64decode(raw, validate=True)
            return json.loads(decoded.decode("utf-8"))
        except (binascii.Error, ValueError, json.JSONDecodeError):
            return None
    return None


def extract_decision(slot):
    """Pull the `decision` field from one analyst's payload slot.

    The host's composition path embeds JSON objects verbatim and
    non-JSON values as JSON strings. A well-formed analyst output is
    a dict carrying `decision`; a missing or malformed slot returns
    the empty string so the divergence check naturally treats it as
    "no signal" rather than crashing.
    """
    if isinstance(slot, dict):
        decision = slot.get("decision", "")
        if isinstance(decision, str):
            return decision.strip().lower()
    return ""


def main():
    req = json.load(sys.stdin)
    payload = decode_payload(req.get("payload"))
    if payload is None:
        json.dump({"pass": False, "reason": "payload is not valid JSON"}, sys.stdout)
        return
    if not isinstance(payload, dict):
        json.dump({"pass": False, "reason": "payload is not a JSON object"}, sys.stdout)
        return

    missing = [a for a in REQUIRED_ANALYSTS if a not in payload]
    if missing:
        json.dump({
            "pass": False,
            "reason": "missing positions from: " + ", ".join(missing),
        }, sys.stdout)
        return

    bull_decision = extract_decision(payload.get("bull"))
    bear_decision = extract_decision(payload.get("bear"))

    if bull_decision and bear_decision and bull_decision == bear_decision:
        json.dump({
            "pass": False,
            "reason": (
                "bull and bear both recommend '{}' "
                "— adversarial review collapsed, re-run"
            ).format(bull_decision),
        }, sys.stdout)
        return

    json.dump({"pass": True}, sys.stdout)


if __name__ == "__main__":
    main()
