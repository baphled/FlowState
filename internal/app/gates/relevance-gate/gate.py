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
