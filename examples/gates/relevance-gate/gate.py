#!/usr/bin/env python3
"""Example external gate that checks researcher output relevance to the task plan."""

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
    words = re.findall(r"\b[a-z]{4,}\b", text.lower())
    return {word for word in words if word not in STOPWORDS}


def coerce_text(value):
    if isinstance(value, str):
        return value
    if value is None:
        return ""
    return json.dumps(value, sort_keys=True)


def decode_payload(raw):
    if raw is None or raw == "":
        return {}
    if isinstance(raw, dict):
        return raw
    if isinstance(raw, str):
        try:
            return json.loads(raw)
        except Exception:
            pass
        try:
            decoded = base64.b64decode(raw, validate=True)
            return json.loads(decoded.decode("utf-8"))
        except (binascii.Error, ValueError, json.JSONDecodeError):
            return None
    return None


def emit(body):
    json.dump(body, sys.stdout)
    sys.stdout.write("\n")


def main():
    try:
        req = json.load(sys.stdin)
    except json.JSONDecodeError as exc:
        emit({"pass": False, "reason": f"invalid request JSON: {exc}"})
        return

    payload = decode_payload(req.get("payload"))
    if payload is None:
        emit({"pass": False, "reason": "payload is not valid JSON"})
        return

    task_text = coerce_text(payload.get("task_plan", ""))
    research_text = coerce_text(payload.get("research", ""))

    if not task_text.strip():
        emit({"pass": False, "reason": "task_plan is empty - cannot assess relevance"})
        return

    if not research_text.strip():
        emit({"pass": False, "reason": "research output is empty"})
        return

    task_keywords = extract_keywords(task_text)
    research_keywords = extract_keywords(research_text)

    if not task_keywords:
        emit({"pass": True, "score": 1.0, "note": "task had no extractable keywords"})
        return

    overlap = task_keywords & research_keywords
    score = len(overlap) / len(task_keywords)
    threshold = float((req.get("policy") or {}).get("threshold", 0.4))

    if score >= threshold:
        emit({"pass": True, "score": round(score, 2), "overlap": sorted(overlap)})
        return

    missing = sorted(task_keywords - research_keywords)
    redirect = "Research should cover: " + ", ".join(missing[:5])
    emit({
        "pass": False,
        "reason": f"research relevance score {score:.2f} below threshold {threshold:.2f}; {redirect}",
        "missing_topics": missing[:10],
        "redirect": redirect,
    })


if __name__ == "__main__":
    main()
