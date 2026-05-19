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
import json
import sys


REQUIRED_ANALYSTS = ["bull", "bear", "market", "financial", "technical"]


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
    # Wire-format invariant — the host's ExtGateRequest.Payload is a
    # json.RawMessage that embeds verbatim into the marshalled stdin,
    # so the value arrives here as a parsed JSON object (composed
    # multi-key payload) or null (no composition produced). The legacy
    # base64-decode fallback retired alongside the wire format change;
    # see internal/swarm/extproc.go ExtGateRequest comment.
    payload = req.get("payload") or {}
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
