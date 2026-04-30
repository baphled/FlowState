#!/usr/bin/env python3
"""
ehcp-completeness gate — validates that all 11 EHCP sections A–K are present
and non-empty in the drafter's output before the compliance checker runs.

Input  (stdin):  JSON  {"kind": "ehcp-completeness", "payload": {"sections": {...}}, "policy": {}}
Output (stdout): JSON  {"pass": true}  or  {"pass": false, "reason": "...", "missing": [...]}

Python 3.9 stdlib only — no third-party dependencies.
"""
import json
import sys

SECTION_NAMES = {
    "A": "Child's/young person's views, interests and aspirations",
    "B": "Special educational needs",
    "C": "Health needs",
    "D": "Social care needs",
    "E": "Outcomes (SMART)",
    "F": "Educational provision",
    "G": "Health provision",
    "H": "Social care provision",
    "I": "Placement",
    "J": "Personal budget",
    "K": "Appendices checklist",
}

REQUIRED_SECTIONS = list("ABCDEFGHIJK")


def main() -> None:
    try:
        req = json.load(sys.stdin)
    except json.JSONDecodeError as exc:
        json.dump({"pass": False, "reason": f"invalid JSON input: {exc}"}, sys.stdout)
        return

    payload = req.get("payload") or {}
    if not isinstance(payload, dict):
        json.dump({"pass": False, "reason": "payload must be a JSON object"}, sys.stdout)
        return

    sections = payload.get("sections") or {}
    if not isinstance(sections, dict):
        json.dump(
            {"pass": False, "reason": "payload.sections must be a JSON object"},
            sys.stdout,
        )
        return

    missing = []
    for letter in REQUIRED_SECTIONS:
        value = sections.get(letter)
        if not value or (isinstance(value, str) and not value.strip()):
            missing.append({"section": letter, "name": SECTION_NAMES[letter]})

    if missing:
        missing_labels = ", ".join(
            f"Section {m['section']} ({m['name']})" for m in missing
        )
        json.dump(
            {
                "pass": False,
                "reason": f"Missing or empty EHCP sections: {missing_labels}",
                "missing": missing,
            },
            sys.stdout,
        )
        return

    json.dump({"pass": True}, sys.stdout)


if __name__ == "__main__":
    main()
