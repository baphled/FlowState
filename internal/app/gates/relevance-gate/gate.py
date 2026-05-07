#!/usr/bin/env python3
"""Relevance gate for the A-Team swarm.

Reads a JSON request on stdin, scores the researcher's output against the
task plan via simple keyword overlap, and writes a JSON response on stdout.

Composed payload shape (see internal/app/gates/relevance-gate/manifest.yml):

    {"task_plan": "<task-plan bytes or object>",
     "research":  "<researcher output bytes or object>"}

Response shape:

    pass=True  -> {"pass": true, "score": 0.83, "overlap": [...]}
    pass=False -> {"pass": false, "reason": "...", "missing_topics": [...],
                   "redirect": "Research should cover: ..."}

The ExtGateResponse decoder on the host side reads `pass` + `reason`
verbatim; `score`, `overlap`, `missing_topics`, and `redirect` are
diagnostic fields preserved in the gate's stdout for operator debugging.
"""
import base64
import binascii
import json
import re
import sys


STOPWORDS = {
    "that", "this", "with", "from", "have", "will", "been", "were",
    "they", "what", "when", "your", "which", "about", "would", "could",
    "should", "their", "there", "some", "into", "also", "than", "then",
}


def extract_keywords(text):
    """Extract meaningful keywords (>=4 chars, no stopwords) from text."""
    words = re.findall(r"\b[a-z]{4,}\b", text.lower())
    return {w for w in words if w not in STOPWORDS}


def coerce_text(value):
    """Normalise a JSON value to flat text for keyword extraction.

    Coord-store payloads may arrive as raw strings or as parsed JSON
    objects (the host's composeMultiKeyPayload embeds JSON values
    verbatim). Strings are returned as-is; everything else is
    JSON-serialised so an object's content still feeds into keyword
    extraction.
    """
    if isinstance(value, str):
        return value
    if value is None:
        return ""
    return json.dumps(value)


def decode_payload(raw):
    """Coerce the request's `payload` field into a Python dict.

    Go's `encoding/json` marshals `[]byte` as a base64 string, so the
    host side's swarm.ExtGateRequest{Payload: <bytes>} arrives here as
    a base64-encoded value. Older example dispatchers (and direct unit
    tests) feed a raw JSON string or a pre-parsed object instead. Try
    each in turn so the gate is portable across both call shapes.
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


def main():
    req = json.load(sys.stdin)
    payload = decode_payload(req.get("payload"))
    if payload is None:
        json.dump({"pass": False, "reason": "payload is not valid JSON"}, sys.stdout)
        return

    task_text = coerce_text(payload.get("task_plan", ""))
    research_text = coerce_text(payload.get("research", ""))

    if not task_text.strip():
        json.dump({"pass": False, "reason": "task_plan is empty - cannot assess relevance"}, sys.stdout)
        return

    if not research_text.strip():
        json.dump({"pass": False, "reason": "research output is empty"}, sys.stdout)
        return

    task_keywords = extract_keywords(task_text)
    research_keywords = extract_keywords(research_text)

    if not task_keywords:
        json.dump({"pass": True, "score": 1.0, "note": "task had no extractable keywords"}, sys.stdout)
        return

    overlap = task_keywords & research_keywords
    score = len(overlap) / len(task_keywords)
    threshold = float((req.get("policy") or {}).get("threshold", 0.4))

    if score >= threshold:
        json.dump({"pass": True, "score": round(score, 2), "overlap": sorted(overlap)}, sys.stdout)
        return

    missing = sorted(task_keywords - research_keywords)
    redirect = "Research should cover: " + ", ".join(missing[:5])
    json.dump({
        "pass": False,
        "reason": (
            "research relevance score {:.2f} below threshold {:.2f}; {}".format(
                score, threshold, redirect,
            )
        ),
        "missing_topics": missing[:10],
        "redirect": redirect,
    }, sys.stdout)


if __name__ == "__main__":
    main()
