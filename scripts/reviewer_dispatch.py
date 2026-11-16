"""Launch a bounded FlowState verifier session; never infer acceptance from output.

This adapter creates a separate *session*, not authenticated actor provenance.
The existing signed review/execution admission contract remains authoritative.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

SHA = re.compile(r"[0-9a-f]{40}\Z")
SESSION = re.compile(r"[A-Za-z0-9_-]{1,128}\Z")


class DispatchError(Exception):
    """Refuse ambiguous or unqualified independent-review execution."""


def ensure_workspace(workspace, repo):
    """Require a private external directory for review state and transcripts."""
    root = Path(workspace).resolve(strict=True)
    checkout = Path(repo).resolve(strict=True)
    if not root.is_dir() or root.is_relative_to(checkout):
        raise DispatchError("review workspace must be a directory outside the checkout")
    if root.stat().st_mode & 0o077:
        raise DispatchError("review workspace must be owner-private (0700)")
    if root.stat().st_uid != os.getuid():
        raise DispatchError("review workspace must belong to the invoking user")
    return root


def native_environment():
    """Inherit provider credentials but discard legacy external-agent hints."""
    return {key: value for key, value in os.environ.items()
            if key != "OPENCODE" and not key.startswith("OPENCODE_")
            and key not in ("AI_AGENT", "AI_MODEL")}


def run_command(argv, timeout):
    """Call the named native binary with literal arguments, never a shell."""
    try:
        result = subprocess.run(argv, check=False, text=True, capture_output=True,
                                timeout=timeout, env=native_environment())
    except (OSError, subprocess.TimeoutExpired) as error:
        raise DispatchError("FlowState verifier failed to start or timed out") from error
    if result.returncode:
        raise DispatchError("FlowState verifier returned a nonzero exit status")
    return result.stdout


def review_task(packet, head, base, pr):
    """Construct a public, bounded, read-only exact-candidate verifier task."""
    body = Path(packet).read_text(encoding="utf-8")
    if not body.strip() or len(body.encode()) > 32768:
        raise DispatchError("review packet is empty or exceeds 32 KiB")
    return (f"Role: verifier; independent review only; repository: baphled/FlowState; "
            f"PR: {pr}; head: {head}; base: {base}.\n"
            "Review the public contract and exact candidate, inspect negative cases, "
            "and report findings. Do not modify sources, call another harness, "
            "or claim merge admission. A reply is not signed approval.\n\n" + body)


def dispatch(repo, workspace, packet, pr, head, base, implementer, model,
             invoke=run_command):
    """Create and run a native FlowState reviewer, retaining untrusted evidence."""
    if pr <= 0 or not SHA.fullmatch(head) or not SHA.fullmatch(base):
        raise DispatchError("review requires a valid PR and exact head/base SHAs")
    if not SESSION.fullmatch(implementer) or not model.strip():
        raise DispatchError("implementer session and explicit model are required")
    work = ensure_workspace(workspace, repo)
    binary = Path(repo).resolve() / "build" / "flowstate"
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise DispatchError("native FlowState binary unavailable; no external fallback")
    task = work / "verifier-task.txt"
    state = work / "verifier.db"
    receipt = work / "verifier-dispatch.json"
    if any(x.exists() for x in (task, state, receipt)):
        raise DispatchError("review workspace already contains an attempt")
    task.write_text(review_task(packet, head, base, pr), encoding="utf-8")
    task.chmod(0o600)
    prefix = [str(binary), "session"]
    raw = invoke(prefix + ["new", "--provider", "zai", "--model", model,
                           "--task", "@" + str(task), "--db", str(state)], 30)
    try:
        reviewer = json.loads(raw)["id"]
    except (ValueError, KeyError, TypeError) as error:
        raise DispatchError("native reviewer did not return a session ID") from error
    if not isinstance(reviewer, str) or not SESSION.fullmatch(reviewer):
        raise DispatchError("invalid native reviewer session identity")
    if reviewer == implementer:
        raise DispatchError("verifier session must differ from implementer")
    invoke(prefix + ["run", reviewer, "--budget", "20000", "--max-tokens", "4096",
                     "--db", str(state)], 300)
    events = invoke(prefix + ["events", reviewer, "--db", str(state)], 30)
    evidence = {"repository": "baphled/FlowState", "pr_number": pr,
                "head_sha": head, "base_sha": base,
                "requested_provider": "zai", "requested_model": model,
                "implementer_session": implementer, "reviewer_session": reviewer,
                "event_log_sha256": hashlib.sha256(events.encode()).hexdigest(),
                "review_accepted": False, "status": "requires_authenticated_independent_review"}
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    fd = os.open(receipt, flags, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as file:
        json.dump(evidence, file, sort_keys=True, indent=2)
        file.write("\n")
    return evidence


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("repo", "workspace", "packet", "head", "base",
                 "implementer-session", "model"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--pr", type=int, required=True)
    args = parser.parse_args(argv)
    try:
        result = dispatch(args.repo, args.workspace, args.packet, args.pr,
                          args.head, args.base, args.implementer_session, args.model)
    except (DispatchError, OSError, UnicodeError) as error:
        print("review dispatch refused: " + str(error), file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
