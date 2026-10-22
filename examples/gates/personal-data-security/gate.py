#!/usr/bin/env python3
"""
personal-data-security gate.

Scans the payload for UK personal data patterns and either redacts the
matches (action=strip, default) or blocks the gate (action=block).

Names are not covered — regex is unreliable for names. NER-based
detection is a future enhancement.

Input (JSON on stdin):
  {
    "payload": "string OR object — both accepted",
    "policy": {"action": "strip"}   # optional
  }

The gate also accepts "content" as an alias for "payload" and reads the
mode from the PDS_MODE environment variable when neither payload key nor
policy is provided.
"""
import json
import os
import re
import sys


# ---------------------------------------------------------------------------
# Compiled patterns (module-level for efficiency)
# ---------------------------------------------------------------------------
PATTERNS = {
    "NI_NUMBER":   re.compile(r"\b[A-CEGHJ-PR-TW-Z]{2}\d{6}[A-D]?\b"),
    "NHS_NUMBER":  re.compile(r"\b\d{3}\s?\d{3}\s?\d{4}\b"),
    "UK_POSTCODE": re.compile(r"\b[A-Z]{1,2}\d[A-Z\d]?\s*\d[A-Z]{2}\b", re.IGNORECASE),
    "EMAIL":       re.compile(r"\b[\w.+-]+@[\w-]+\.[\w.-]+\b"),
    "UK_PHONE":    re.compile(r"\b(?:\+44\s?\d{4}|\(?0\d{2,4}\)?)\s?\d{3,4}\s?\d{3,4}\b"),
    "DOB":         re.compile(r"\b\d{1,2}[/-]\d{1,2}[/-](?:19|20)\d{2}\b"),
    "IBAN":        re.compile(r"\b[A-Z]{2}\d{2}[A-Z0-9]{4}\d{7}([A-Z0-9]?){0,16}\b"),
}


def flatten(payload) -> str:
    """Flatten payload (str/dict/list) into a single scannable string."""
    if isinstance(payload, str):
        return payload
    return json.dumps(payload, ensure_ascii=False)


def scan(text: str) -> list:
    """Return list of (category, match_text, span) tuples."""
    findings = []
    for category, pattern in PATTERNS.items():
        for m in pattern.finditer(text):
            findings.append((category, m.group(0), m.span()))
    return findings


def redact(text: str, findings: list) -> str:
    """Replace each finding with [REDACTED:<category>] using position-safe substitution."""
    if not findings:
        return text
    # Sort by start position descending so replacements don't shift later spans.
    findings_sorted = sorted(findings, key=lambda f: f[2][0], reverse=True)
    out = text
    for category, _match, (start, end) in findings_sorted:
        out = out[:start] + f"[REDACTED:{category}]" + out[end:]
    return out


def main() -> None:
    req = json.load(sys.stdin)

    # Accept both "payload" and "content" as the input key
    payload = req.get("payload")
    if payload is None:
        payload = req.get("content", "")

    # Resolve action/mode (multiple sources, in priority order):
    #   1. top-level "mode" key in the request (task-instruction format)
    #   2. policy.action key (vault-fact-check-style format)
    #   3. PDS_MODE environment variable
    #   4. default: "strip"
    policy = req.get("policy") or {}
    action = (
        req.get("mode")
        or policy.get("action")
        or policy.get("mode")
        or os.environ.get("PDS_MODE")
        or "strip"
    )
    action = action.lower()

    text = flatten(payload)
    findings = scan(text)

    if not findings:
        if action == "block":
            json.dump({
                "pass": True,
                "categories_checked": list(PATTERNS.keys()),
            }, sys.stdout)
        else:
            json.dump({"pass": True}, sys.stdout)
        return

    # Aggregate findings by category
    counts: dict = {}
    for category, _match, _span in findings:
        counts[category] = counts.get(category, 0) + 1
    summary = [{"category": c, "count": n} for c, n in sorted(counts.items())]

    if action == "block":
        examples = [m for _c, m, _s in findings[:5]]
        categories_found = list(dict.fromkeys(c for c, _m, _s in findings))
        json.dump({
            "pass": False,
            "reason": "personal data detected",
            "categories": categories_found,
            "examples": examples,
        }, sys.stdout)
        return

    # Default: strip
    redacted = redact(text, findings)
    redactions = [
        {"category": c, "original": m, "position": s[0]}
        for c, m, s in sorted(findings, key=lambda f: f[2][0])
    ]
    json.dump({
        "pass": True,
        "output": redacted,
        "redactions": redactions,
    }, sys.stdout)


if __name__ == "__main__":
    main()
