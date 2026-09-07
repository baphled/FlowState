#!/usr/bin/env python3
"""TDD + commit discipline gate for the engineer sub-swarm.

Validates that an engineer's output adheres to:

1. TDD discipline — evidence of a RED (failing test) phase written
   BEFORE any implementation code (GREEN phase).
2. Small commits — work committed through `make ai-commit`, with a
   valid commit SHA and reasonable file count per commit.
3. No raw `git commit` — the commit path must go through the
   project-mandated `make ai-commit` wrapper.

Input (composed from coord-store):

    {"payload": {"output": "<member's JSON output>"}}

The member's output is a JSON object shaped as:

    {
      "tdd_phases": {
        "red": {
          "test_file": "path/to/test.go",
          "test_description": "It should ...",
          "failing_output": "..."
        },
        "green": {
          "implementation_files": ["path/to/impl.go"],
          "passing_output": "..."
        },
        "refactor": {
          "files_changed": ["..."],
          "passing_output": "..."
        }
      },
      "commits": [
        {
          "sha": "abc1234",
          "message": "feat(scope): description",
          "files_changed": ["path/to/test.go"],
          "via_ai_commit": true
        }
      ]
    }

Response shape:

    pass=True  -> {"pass": true, "phases_seen": ["red", "green"],
                   "commits_validated": 2}
    pass=False -> {"pass": false, "reason": "...", "failures": [...]}
"""
import json
import sys


def decode_payload(raw):
    """Coerce the request's payload field into a Python dict.

    Handles base64-encoded bytes (Go's json.Marshal of []byte), raw
    JSON strings, and pre-parsed dicts. Mirrors the decode pattern
    from relevance-gate and quorum-gate.
    """
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


def validate_tdd_phases(output, policy):
    """Check that RED and GREEN phases are present and well-formed."""
    failures = []
    require_red = policy.get("require_red_phase", True)
    require_green = policy.get("require_green_phase", True)

    phases = output.get("tdd_phases", {})

    if require_red:
        red = phases.get("red")
        if not red:
            failures.append({
                "check": "tdd_red_phase",
                "reason": "no RED phase evidence found; a failing test must be written before implementation",
            })
        elif not red.get("test_file"):
            failures.append({
                "check": "tdd_red_phase",
                "reason": "RED phase missing test_file; specify the file containing the failing test",
            })
        elif not red.get("failing_output"):
            failures.append({
                "check": "tdd_red_phase",
                "reason": "RED phase missing failing_output; provide evidence the test failed before implementation",
            })

    if require_green:
        green = phases.get("green")
        if not green:
            failures.append({
                "check": "tdd_green_phase",
                "reason": "no GREEN phase evidence found; implementation must make the RED test pass",
            })
        elif not green.get("implementation_files"):
            failures.append({
                "check": "tdd_green_phase",
                "reason": "GREEN phase missing implementation_files; list files changed to make the test pass",
            })
        elif not green.get("passing_output"):
            failures.append({
                "check": "tdd_green_phase",
                "reason": "GREEN phase missing passing_output; provide evidence the test now passes",
            })

    phases_seen = [k for k in ("red", "green", "refactor") if k in phases]
    return failures, phases_seen


def validate_commits(output, policy):
    """Check commit discipline: SHA present, via ai-commit, small scope."""
    failures = []
    require_sha = policy.get("require_commit_sha", True)
    require_ai = policy.get("require_ai_commit", True)
    max_files = policy.get("max_files_per_commit", 5)

    commits = output.get("commits", [])
    if not commits:
        failures.append({
            "check": "commit_evidence",
            "reason": "no commits reported; every TDD cycle must produce a commit via make ai-commit",
        })
        return failures, 0

    for i, commit in enumerate(commits):
        idx = i + 1
        if require_sha:
            sha = commit.get("sha", "").strip()
            if not sha:
                failures.append({
                    "check": "commit_sha",
                    "reason": f"commit {idx} missing SHA; provide the full or short commit hash",
                })

        if require_ai:
            via_ai = commit.get("via_ai_commit")
            if via_ai is False:
                failures.append({
                    "check": "commit_path",
                    "reason": f"commit {idx} was not made via make ai-commit; all commits must use the mandated path",
                })
            elif via_ai is None:
                failures.append({
                    "check": "commit_path",
                    "reason": f"commit {idx} missing via_ai_commit field; explicitly set true/false",
                })

        msg = commit.get("message", "").strip()
        if not msg:
            failures.append({
                "check": "commit_message",
                "reason": f"commit {idx} missing commit message",
            })

        files = commit.get("files_changed", [])
        if max_files and len(files) > max_files:
            failures.append({
                "check": "commit_size",
                "reason": f"commit {idx} changes {len(files)} files (max {max_files}); split into smaller commits",
                "files": files,
            })

    return failures, len(commits)


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
    tdd_failures, phases_seen = validate_tdd_phases(output, policy)
    commit_failures, commits_validated = validate_commits(output, policy)
    all_failures.extend(tdd_failures)
    all_failures.extend(commit_failures)

    if all_failures:
        reasons = "; ".join(f["reason"] for f in all_failures[:3])
        json.dump({
            "pass": False,
            "reason": f"{len(all_failures)} check(s) failed: {reasons}",
            "failures": all_failures,
        }, sys.stdout)
    else:
        json.dump({
            "pass": True,
            "phases_seen": phases_seen,
            "commits_validated": commits_validated,
        }, sys.stdout)


if __name__ == "__main__":
    main()
