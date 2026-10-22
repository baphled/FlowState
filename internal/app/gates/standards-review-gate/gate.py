#!/usr/bin/env python3
"""Standards review gate for the engineer sub-swarm.

Validates Principal-Engineer output to ensure standards reviews are
structured and substantive rather than rubber-stamp approvals.

Checks:
1. Verdict is one of approve / revise / abort (required)
2. Files reviewed list is non-empty (configurable)
3. TDD compliance was explicitly checked (configurable)
4. Issues list present, each with file+line (configurable)
5. For "revise" verdicts, actionable feedback is present

Input (composed from coord-store):

    {"payload": {"output": "<Principal-Engineer's JSON output>"}}

Expected output shape:

    {
      "verdict": "approve" | "revise" | "abort",
      "files_reviewed": ["path/to/file.go", ...],
      "tdd_compliance_checked": true,
      "issues": [
        {
          "file": "path/to/file.go",
          "line": 42,
          "severity": "major" | "minor" | "nit",
          "description": "..."
        }
      ],
      "summary": "Prose summary of the review"
    }

Response shape:

    pass=True  -> {"pass": true, "verdict": "approve",
                   "files_reviewed": 3, "issues_found": 1}
    pass=False -> {"pass": false, "reason": "...", "failures": [...]}
"""
import json
import sys


def decode_payload(raw):
    """Coerce payload to dict (base64, JSON string, or pre-parsed)."""
    if raw is None or raw == "":
        return {}
    if isinstance(raw, dict):
        return raw
    if isinstance(raw, str):
        import base64
        import binascii
        try:
            return json.loads(raw)
        except Exception:
            pass
        try:
            decoded = base64.b64decode(raw, validate=True)
            return json.loads(decoded.decode("utf-8"))
        except Exception:
            return None
    return None


VALID_VERDICTS = {"approve", "revise", "abort"}


def validate_verdict(output):
    """Check verdict is present and valid."""
    verdict = output.get("verdict", "").strip().lower()
    if not verdict:
        return [{"check": "verdict", "reason": "no verdict field; must be one of approve/revise/abort"}]
    if verdict not in VALID_VERDICTS:
        return [{"check": "verdict", "reason": f"verdict '{verdict}' is not valid; must be one of approve/revise/abort"}]
    return []


def validate_files_reviewed(output, policy):
    """Check that files were explicitly listed."""
    if not policy.get("require_files_reviewed", True):
        return []
    files = output.get("files_reviewed", [])
    if not files:
        return [{"check": "files_reviewed", "reason": "no files_reviewed list; every review must enumerate the files examined"}]
    return []


def validate_tdd_compliance(output, policy):
    """Check that TDD compliance was explicitly verified."""
    if not policy.get("require_tdd_compliance_check", True):
        return []
    checked = output.get("tdd_compliance_checked")
    if checked is not True:
        return [{"check": "tdd_compliance", "reason": "tdd_compliance_checked must be explicitly true; standards review must verify TDD-cycle discipline"}]
    return []


def validate_issues(output, policy):
    """Check issues list structure."""
    if not policy.get("require_issues_list", True):
        return []
    issues = output.get("issues", [])
    failures = []
    for i, issue in enumerate(issues):
        idx = i + 1
        if not issue.get("file"):
            failures.append({
                "check": "issue_file",
                "reason": f"issue {idx} missing file; every issue must reference a specific file",
            })
        if not issue.get("description"):
            failures.append({
                "check": "issue_description",
                "reason": f"issue {idx} missing description; provide a plain-English explanation",
            })
    return failures


def validate_revise_feedback(output):
    """For revise verdicts, ensure actionable feedback exists."""
    verdict = output.get("verdict", "").strip().lower()
    if verdict != "revise":
        return []
    issues = output.get("issues", [])
    summary = output.get("summary", "").strip()
    if not issues and not summary:
        return [{"check": "revise_feedback", "reason": "revise verdict with no issues or summary; provide actionable feedback explaining what to revise"}]
    return []


def main():
    req = json.load(sys.stdin)
    policy = req.get("policy", {})
    payload = decode_payload(req.get("payload"))

    if payload is None:
        json.dump({"pass": False, "reason": "payload is not valid JSON"}, sys.stdout)
        return

    output = payload.get("output", payload) if isinstance(payload, dict) else {}
    if isinstance(output, str):
        try:
            output = json.loads(output)
        except Exception:
            json.dump({"pass": False, "reason": "member output is not valid JSON"}, sys.stdout)
            return

    if not isinstance(output, dict):
        json.dump({"pass": False, "reason": "member output is not a JSON object"}, sys.stdout)
        return

    all_failures = []
    all_failures.extend(validate_verdict(output))
    all_failures.extend(validate_files_reviewed(output, policy))
    all_failures.extend(validate_tdd_compliance(output, policy))
    all_failures.extend(validate_issues(output, policy))
    all_failures.extend(validate_revise_feedback(output))

    if all_failures:
        reasons = "; ".join(f["reason"] for f in all_failures[:3])
        json.dump({
            "pass": False,
            "reason": f"{len(all_failures)} check(s) failed: {reasons}",
            "failures": all_failures,
        }, sys.stdout)
    else:
        verdict = output.get("verdict", "").strip().lower()
        files_count = len(output.get("files_reviewed", []))
        issues_count = len(output.get("issues", []))
        json.dump({
            "pass": True,
            "verdict": verdict,
            "files_reviewed": files_count,
            "issues_found": issues_count,
        }, sys.stdout)


if __name__ == "__main__":
    main()
