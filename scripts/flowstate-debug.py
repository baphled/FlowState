#!/usr/bin/env python3
"""One-shot FlowState diagnostic synthesizer.

Aggregates the raw signals other debug scripts surface (session JSON,
log, process state, config) and produces a single classification of
what went wrong, aligned to the runbook in
`Documentation/Guides/Diagnosing Stalled Sessions from Persisted JSON`.

Outputs four buckets of facts:
  1. Session shape: msg count, role breakdown, last-message classification
  2. Tool activity: which tools fired, did the LLMCritic actually run
  3. Config-derived expectations: critic_enabled, timeouts, provider
  4. Live signals: process state, recent log tail

Then a single VERDICT line: backend-completed / engine-wedged /
provider-died / process-crashed / inconclusive.

Usage:
    scripts/flowstate-debug.py                 # diagnose latest session
    scripts/flowstate-debug.py <session-id>    # diagnose a specific session
    scripts/flowstate-debug.py --json          # machine-readable output
    scripts/flowstate-debug.py --no-color      # disable ANSI colour
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

SESSIONS_DIR = Path.home() / ".local" / "share" / "flowstate" / "sessions"
LOG_PATH = Path.home() / ".local" / "share" / "flowstate" / "flowstate.log"
CONFIG_PATH = Path.home() / ".config" / "flowstate" / "config.yaml"

# These match the in-repo defaults in internal/engine/engine.go (lines 34–35)
# and internal/engine/background_output.go (line 169) at HEAD f917f8d
# (2026-04-25). Kept here so the script can flag drift without parsing Go.
DEFAULT_STREAM_TIMEOUT_MIN = 5
DEFAULT_TOOL_TIMEOUT_MIN = 2
DEFAULT_BACKGROUND_OUTPUT_TIMEOUT_S = 120

COLOURS = {
    "reset": "\033[0m",
    "bold": "\033[1m",
    "dim": "\033[2m",
    "red": "\033[31m",
    "green": "\033[32m",
    "yellow": "\033[33m",
    "blue": "\033[34m",
    "magenta": "\033[35m",
    "cyan": "\033[36m",
}


@dataclass
class Diagnosis:
    session_id: str
    session_path: Path
    file_size_bytes: int
    message_count: int
    role_counts: dict[str, int]
    last_message: dict[str, Any]
    tool_call_frequency: dict[str, int]
    critic_invoked: bool
    config: dict[str, Any]
    process: dict[str, Any]
    log_tail: list[str]
    verdict: str = ""
    rationale: list[str] = field(default_factory=list)
    suggested_next_steps: list[str] = field(default_factory=list)


def latest_session() -> Path:
    files = list(SESSIONS_DIR.glob("*.json"))
    if not files:
        raise SystemExit(f"no sessions in {SESSIONS_DIR}")
    return max(files, key=lambda p: p.stat().st_mtime)


def resolve_session(session_arg: str | None) -> Path:
    if session_arg is None:
        return latest_session()
    candidate = SESSIONS_DIR / f"{session_arg}.json"
    if candidate.exists():
        return candidate
    # Allow passing an absolute path or a partial UUID prefix.
    p = Path(session_arg)
    if p.exists():
        return p
    matches = [f for f in SESSIONS_DIR.glob("*.json") if f.stem.startswith(session_arg)]
    if len(matches) == 1:
        return matches[0]
    if len(matches) > 1:
        raise SystemExit(f"ambiguous session prefix {session_arg!r}: matches {[m.stem for m in matches]}")
    raise SystemExit(f"no session matching {session_arg!r} in {SESSIONS_DIR}")


def load_session(path: Path) -> dict[str, Any]:
    with path.open() as f:
        return json.load(f)


def classify_messages(messages: list[dict[str, Any]]) -> tuple[dict[str, int], dict[str, Any]]:
    role_counts: dict[str, int] = {}
    last_msg: dict[str, Any] = {}
    for envelope in messages:
        msg = envelope.get("message", {}) or {}
        role = msg.get("Role") or msg.get("role") or "unknown"
        role_counts[role] = role_counts.get(role, 0) + 1
    if messages:
        msg = messages[-1].get("message", {}) or {}
        content = msg.get("Content") or msg.get("content") or ""
        tool_calls = msg.get("ToolCalls") or msg.get("tool_calls") or []
        thinking = msg.get("Thinking") or msg.get("thinking") or ""
        last_msg = {
            "role": msg.get("Role") or msg.get("role") or "unknown",
            "content_length": len(content),
            "content_preview": content[:200],
            "content_tail": content[-200:] if len(content) > 200 else "",
            "tool_call_count": len(tool_calls) if isinstance(tool_calls, list) else 0,
            "thinking_length": len(thinking),
        }
    return role_counts, last_msg


def collect_tool_frequency(messages: list[dict[str, Any]]) -> tuple[dict[str, int], bool]:
    """Count tool-call names across the session and report whether
    anything that looks like an LLMCritic invocation appears.

    The critic runs in-process inside the harness rather than as a
    distinct tool call, so we look both at top-level tool names and
    at the raw JSON for the `harness_critic_feedback` event marker
    that the harness emits when a critic verdict lands.
    """
    freq: dict[str, int] = {}
    critic_seen = False
    critic_markers = (
        "harness_critic_feedback",
        "LLMCritic",
        "CriticVerdict",
        "VerdictPass",
        "VerdictFail",
    )
    for envelope in messages:
        msg = envelope.get("message", {}) or {}
        tool_calls = msg.get("ToolCalls") or msg.get("tool_calls") or []
        if isinstance(tool_calls, list):
            for tc in tool_calls:
                name = (
                    tc.get("function", {}).get("name")
                    if isinstance(tc.get("function"), dict)
                    else tc.get("name")
                )
                if name:
                    freq[name] = freq.get(name, 0) + 1
        # Cheap text scan for critic markers that live in content/event payloads.
        content = msg.get("Content") or msg.get("content") or ""
        if isinstance(content, str):
            for marker in critic_markers:
                if marker in content:
                    critic_seen = True
                    break
    return freq, critic_seen


def read_user_config() -> dict[str, Any]:
    """Pull only the keys we report on. PyYAML isn't always installed,
    so do a narrow line-by-line probe — good enough for the keys we
    care about and avoids forcing a dependency on the diagnostic.
    """
    cfg: dict[str, Any] = {
        "exists": CONFIG_PATH.exists(),
        "critic_enabled": None,
        "stream_timeout": None,
        "tool_timeout": None,
        "background_output_timeout": None,
        "default_provider": None,
    }
    if not CONFIG_PATH.exists():
        return cfg
    text = CONFIG_PATH.read_text()
    for line in text.splitlines():
        s = line.strip()
        if s.startswith("#"):
            continue
        if s.startswith("critic_enabled:"):
            cfg["critic_enabled"] = s.split(":", 1)[1].strip()
        elif s.startswith("stream_timeout:"):
            cfg["stream_timeout"] = s.split(":", 1)[1].strip()
        elif s.startswith("tool_timeout:"):
            cfg["tool_timeout"] = s.split(":", 1)[1].strip()
        elif s.startswith("background_output_timeout:"):
            cfg["background_output_timeout"] = s.split(":", 1)[1].strip()
        elif s.startswith("default:") and "providers" in text[:text.find(line)]:
            cfg["default_provider"] = s.split(":", 1)[1].strip()
    return cfg


def check_process() -> dict[str, Any]:
    """pgrep + ps for any running flowstate processes. Best-effort."""
    out: dict[str, Any] = {"running": False, "processes": []}
    if not shutil.which("pgrep"):
        return out
    try:
        res = subprocess.run(
            ["pgrep", "-fa", "flowstate"],
            capture_output=True,
            text=True,
            timeout=2,
        )
    except (subprocess.TimeoutExpired, OSError):
        return out
    if res.returncode != 0:
        return out
    out["running"] = True
    for line in res.stdout.strip().splitlines():
        parts = line.split(None, 1)
        if len(parts) < 2:
            continue
        pid, cmd = parts[0], parts[1]
        # Pull cpu/etime from ps for more colour.
        try:
            ps = subprocess.run(
                ["ps", "-p", pid, "-o", "pid,pcpu,pmem,etime,stat,comm"],
                capture_output=True,
                text=True,
                timeout=2,
            )
            ps_lines = ps.stdout.strip().splitlines()
            ps_data = ps_lines[1].split() if len(ps_lines) > 1 else []
        except (subprocess.TimeoutExpired, OSError):
            ps_data = []
        out["processes"].append(
            {
                "pid": pid,
                "cmd": cmd,
                "cpu": ps_data[1] if len(ps_data) > 1 else "?",
                "mem": ps_data[2] if len(ps_data) > 2 else "?",
                "etime": ps_data[3] if len(ps_data) > 3 else "?",
                "state": ps_data[4] if len(ps_data) > 4 else "?",
            }
        )
    return out


def tail_log(session_id: str, lines: int = 60) -> list[str]:
    if not LOG_PATH.exists():
        return []
    # Pull the whole file, filter for the session id, keep the last N.
    try:
        text = LOG_PATH.read_text(errors="replace")
    except OSError:
        return []
    matched = [ln for ln in text.splitlines() if session_id in ln]
    return matched[-lines:]


def derive_verdict(d: Diagnosis) -> tuple[str, list[str], list[str]]:
    """Return (verdict, rationale_lines, suggested_next_steps).

    The classification follows the runbook decision tree in §3.2.
    """
    last = d.last_message
    role = last.get("role", "")
    content_len = last.get("content_length", 0)
    tool_calls = last.get("tool_call_count", 0)

    rationale: list[str] = []
    suggested: list[str] = []

    if not d.process["running"]:
        rationale.append("flowstate process is not running.")
        return "process-stopped", rationale, [
            "Check flowstate.log for the last lines before exit.",
            "Restart the TUI/server and re-load the session.",
        ]

    if role == "assistant" and content_len > 0 and tool_calls == 0:
        rationale.append(
            f"Last message is an assistant turn with {content_len} chars and 0 pending tool calls."
        )
        rationale.append("Backend committed the response. The TUI render is the suspect.")
        suggested.append("Inspect the TUI viewport / glamour render path for very long content.")
        suggested.append("Check log for the chunk burst size on the final stream.")
        return "backend-completed-render-failed", rationale, suggested

    if role == "assistant" and content_len == 0 and tool_calls > 0:
        rationale.append(
            f"Last message is an assistant turn with 0 chars and {tool_calls} pending tool calls."
        )
        rationale.append("Engine emitted tool calls and is waiting on results.")
        suggested.append("Look at messages[-2] and check whether a tool result was meant to come back.")
        suggested.append("Check engine run-loop wakeup on tool-result delivery.")
        return "engine-wedged-on-tools", rationale, suggested

    if role == "tool":
        rationale.append("Last message is a tool result; engine should have looped but didn't.")
        suggested.append("Check engine.runStreamEvaluation retry logic and context cancellation paths.")
        return "engine-wedged-after-tool-result", rationale, suggested

    if role == "user":
        rationale.append("Last message is the user's prompt; the model produced nothing.")
        suggested.append("Check provider stream errors in flowstate.log around the session start time.")
        suggested.append("Verify provider creds and network reachability.")
        return "provider-stream-died", rationale, suggested

    rationale.append(f"Last message role {role!r} doesn't match any known stall pattern.")
    return "inconclusive", rationale, ["Open the session JSON and walk the last 5 messages by hand."]


def render_section(title: str, body: str, *, colour: str, no_color: bool) -> str:
    if no_color:
        return f"\n=== {title} ===\n{body}\n"
    return f"\n{COLOURS['bold']}{COLOURS[colour]}=== {title} ==={COLOURS['reset']}\n{body}\n"


def render_text(d: Diagnosis, no_color: bool) -> str:
    out: list[str] = []

    # ---- Session shape ----
    body = (
        f"id:           {d.session_id}\n"
        f"path:         {d.session_path}\n"
        f"size:         {d.file_size_bytes:,} bytes\n"
        f"messages:     {d.message_count}\n"
        f"role counts:  {dict(sorted(d.role_counts.items()))}"
    )
    out.append(render_section("Session shape", body, colour="cyan", no_color=no_color))

    # ---- Last message ----
    last = d.last_message
    body = (
        f"role:                {last.get('role')}\n"
        f"content length:      {last.get('content_length'):,} chars\n"
        f"tool calls pending:  {last.get('tool_call_count')}\n"
        f"thinking length:     {last.get('thinking_length'):,} chars\n"
        f"--- preview (first 200 chars) ---\n"
        f"{last.get('content_preview', '').rstrip()}\n"
        f"--- tail (last 200 chars) ---\n"
        f"{last.get('content_tail', '').rstrip()}"
    )
    out.append(render_section("Last message", body, colour="cyan", no_color=no_color))

    # ---- Tool activity + critic ----
    freq = d.tool_call_frequency
    if freq:
        freq_lines = "\n".join(f"  {count:>4}  {name}" for name, count in sorted(freq.items(), key=lambda kv: -kv[1]))
    else:
        freq_lines = "  (no tool calls recorded in any message)"
    critic_status = "YES (markers found)" if d.critic_invoked else "NO (not detected)"
    critic_warn = ""
    if not d.critic_invoked:
        cfg_critic = d.config.get("critic_enabled")
        if cfg_critic in (None, "false"):
            critic_warn = (
                "\n  ⚠ critic_enabled is not set in ~/.config/flowstate/config.yaml.\n"
                "    Default is false — LLMCritic will never run.\n"
                "    To enable, add `harness:\\n  critic_enabled: true` (or whatever\n"
                "    the harness config block uses in your config schema)."
            )
    body = f"tool call frequency:\n{freq_lines}\n\nLLMCritic invoked: {critic_status}{critic_warn}"
    out.append(render_section("Tool activity + critic", body, colour="magenta", no_color=no_color))

    # ---- Config + expected timeouts ----
    cfg = d.config
    body = (
        f"~/.config/flowstate/config.yaml exists:  {cfg['exists']}\n"
        f"default provider:                        {cfg.get('default_provider') or '(default zai/glm-4.7)'}\n"
        f"critic_enabled:                          {cfg.get('critic_enabled') or 'false (default)'}\n"
        f"stream_timeout:                          {cfg.get('stream_timeout') or f'{DEFAULT_STREAM_TIMEOUT_MIN}m (default, NOT YAML-configurable)'}\n"
        f"tool_timeout:                            {cfg.get('tool_timeout') or f'{DEFAULT_TOOL_TIMEOUT_MIN}m (default; delegate tool exempt via TimeoutOverrider)'}\n"
        f"background_output timeout:               {cfg.get('background_output_timeout') or f'{DEFAULT_BACKGROUND_OUTPUT_TIMEOUT_S}s (default, hardcoded in background_output.go)'}"
    )
    out.append(render_section("Config + timeouts", body, colour="yellow", no_color=no_color))

    # ---- Process state ----
    proc = d.process
    if proc["running"]:
        rows = ["pid     cpu    mem   etime      state  cmd"]
        for p in proc["processes"]:
            rows.append(f"{p['pid']:<7} {p['cpu']:<6} {p['mem']:<5} {p['etime']:<10} {p['state']:<6} {p['cmd'][:60]}")
        body = "\n".join(rows)
    else:
        body = "no flowstate processes running"
    out.append(render_section("Process state", body, colour="blue", no_color=no_color))

    # ---- Log tail ----
    if d.log_tail:
        body = "\n".join(d.log_tail[-25:])
    else:
        body = "(no log lines mention this session id)"
    out.append(render_section(f"Log tail ({len(d.log_tail)} matching lines, showing last 25)", body, colour="dim", no_color=no_color))

    # ---- Verdict ----
    verdict_colour = {
        "backend-completed-render-failed": "yellow",
        "engine-wedged-on-tools": "red",
        "engine-wedged-after-tool-result": "red",
        "provider-stream-died": "red",
        "process-stopped": "red",
        "inconclusive": "magenta",
    }.get(d.verdict, "magenta")
    body_lines = [f"VERDICT: {d.verdict}", ""]
    body_lines += [f"  • {r}" for r in d.rationale]
    if d.suggested_next_steps:
        body_lines.append("")
        body_lines.append("Suggested next steps:")
        body_lines += [f"  → {s}" for s in d.suggested_next_steps]
    out.append(render_section("Diagnosis", "\n".join(body_lines), colour=verdict_colour, no_color=no_color))

    return "".join(out)


def render_json(d: Diagnosis) -> str:
    return json.dumps(
        {
            "session_id": d.session_id,
            "session_path": str(d.session_path),
            "file_size_bytes": d.file_size_bytes,
            "message_count": d.message_count,
            "role_counts": d.role_counts,
            "last_message": d.last_message,
            "tool_call_frequency": d.tool_call_frequency,
            "critic_invoked": d.critic_invoked,
            "config": d.config,
            "process": d.process,
            "log_tail_count": len(d.log_tail),
            "verdict": d.verdict,
            "rationale": d.rationale,
            "suggested_next_steps": d.suggested_next_steps,
            "generated_at": datetime.now(timezone.utc).isoformat(),
        },
        indent=2,
    )


def main() -> int:
    parser = argparse.ArgumentParser(
        description="One-shot FlowState session diagnosis (synthesizes session JSON + log + config + process state)."
    )
    parser.add_argument(
        "session",
        nargs="?",
        help="Session UUID (or prefix, or absolute path). Defaults to the latest session.",
    )
    parser.add_argument("--json", action="store_true", help="Emit machine-readable JSON.")
    parser.add_argument("--no-color", action="store_true", help="Disable ANSI colour.")
    args = parser.parse_args()

    no_color = args.no_color or not sys.stdout.isatty() or os.environ.get("NO_COLOR")

    path = resolve_session(args.session)
    data = load_session(path)
    messages = data.get("messages", [])
    role_counts, last_msg = classify_messages(messages)
    freq, critic_seen = collect_tool_frequency(messages)

    d = Diagnosis(
        session_id=data.get("session_id", path.stem),
        session_path=path,
        file_size_bytes=path.stat().st_size,
        message_count=len(messages),
        role_counts=role_counts,
        last_message=last_msg,
        tool_call_frequency=freq,
        critic_invoked=critic_seen,
        config=read_user_config(),
        process=check_process(),
        log_tail=tail_log(data.get("session_id", path.stem)),
    )
    d.verdict, d.rationale, d.suggested_next_steps = derive_verdict(d)

    if args.json:
        print(render_json(d))
    else:
        print(render_text(d, no_color=bool(no_color)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
