#!/usr/bin/env python3
"""Cross-reference FlowState sessions, recordings, and logs for debugging.

Produces a unified debug report correlating:
- Session metadata and messages
- Session recordings (JSONL event traces)
- Application logs (filtered by time window)

Usage:
    scripts/correlate-debug.py <session-id>
    scripts/correlate-debug.py <session-id> --include-logs
    scripts/correlate-debug.py <session-id> --include-children
    scripts/correlate-debug.py --parent <parent-session-id>
    scripts/correlate-debug.py --latest
    scripts/correlate-debug.py --errors
"""

import argparse
import json
import re
import sys
from collections import Counter, defaultdict
from datetime import datetime, timedelta
from pathlib import Path

RECORDINGS_DIR = Path.home() / ".cache" / "flowstate" / "session-recordings"
SESSIONS_DIR = Path.home() / ".local" / "share" / "flowstate" / "sessions"
LOG_PATH = Path.home() / ".local" / "share" / "flowstate" / "flowstate.log"

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

LOG_LINE_RE = re.compile(r'^time=(\S+)\s+level=(\w+)\s+msg="([^"]*)"(.*)$')


def c(colour, text):
    if not sys.stdout.isatty():
        return str(text)
    return f"{COLOURS.get(colour, '')}{text}{COLOURS['reset']}"


def parse_timestamp(ts):
    try:
        return datetime.fromisoformat(ts)
    except ValueError:
        clean = ts[:26] + ts[-6:]
        return datetime.fromisoformat(clean)


def load_session_meta(session_id):
    path = SESSIONS_DIR / f"{session_id}.meta.json"
    if path.exists():
        with open(path) as f:
            return json.load(f)
    return None


def load_session_data(session_id):
    path = SESSIONS_DIR / f"{session_id}.json"
    if path.exists():
        with open(path) as f:
            return json.load(f)
    return None


def load_recording(session_id):
    path = RECORDINGS_DIR / f"{session_id}.jsonl"
    if not path.exists():
        return []
    events = []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return events


def load_logs_in_window(start_dt, end_dt, margin_seconds=30):
    if not LOG_PATH.exists():
        return []

    window_start = start_dt - timedelta(seconds=margin_seconds)
    window_end = end_dt + timedelta(seconds=margin_seconds)

    entries = []
    with open(LOG_PATH) as f:
        for line in f:
            match = LOG_LINE_RE.match(line.strip())
            if not match:
                continue
            ts_str, level, msg, rest = match.groups()
            try:
                ts = datetime.fromisoformat(ts_str)
            except ValueError:
                continue

            if ts.replace(tzinfo=None) < window_start.replace(tzinfo=None):
                continue
            if ts.replace(tzinfo=None) > window_end.replace(tzinfo=None):
                break

            entries.append(
                {
                    "timestamp": ts,
                    "level": level,
                    "msg": msg,
                    "raw": line.strip(),
                }
            )

    return entries


def find_children(parent_id):
    children = []
    for path in SESSIONS_DIR.glob("*.meta.json"):
        try:
            with open(path) as f:
                meta = json.load(f)
                if meta.get("parent_id") == parent_id:
                    children.append(meta)
        except (json.JSONDecodeError, OSError):
            continue
    return sorted(children, key=lambda m: m.get("created_at", ""))


def find_latest_session():
    latest = None
    latest_ts = ""
    for path in SESSIONS_DIR.glob("*.meta.json"):
        try:
            with open(path) as f:
                meta = json.load(f)
                created = meta.get("created_at", "")
                if created > latest_ts:
                    latest_ts = created
                    latest = meta
        except (json.JSONDecodeError, OSError):
            continue
    return latest


# Patterns that look like errors in Go source code (not actual runtime errors)
_SOURCE_CODE_PATTERNS = re.compile(
    r'(?:^package\s+\w+|^import\s*\(|"errors"|var\s+Err\w+\s*=)',
    re.MULTILINE,
)

# Patterns that indicate genuine runtime/tool errors
_REAL_ERROR_PATTERNS = re.compile(
    r"(?:Error:|error:|ERROR|FAILED|Failed|"
    r"no such file or directory|permission denied|"
    r"command not found|exit status \d+|"
    r"timed out|connection refused|panic:)",
    re.IGNORECASE,
)


def _is_real_error(result):
    if not result:
        return False
    snippet = result[:300]
    if _SOURCE_CODE_PATTERNS.search(snippet):
        return False
    return bool(_REAL_ERROR_PATTERNS.search(snippet))


def analyse_recording_events(events):
    stats = {
        "total_events": len(events),
        "provider_requests": 0,
        "provider_responses": 0,
        "tool_calls": [],
        "tool_results": [],
        "reasoning_steps": [],
        "models": set(),
        "errors_detected": [],
        "first_ts": None,
        "last_ts": None,
    }

    for event in events:  # noqa: PLR0912
        et = event.get("event_type", "")
        data = event.get("data", {})
        ts = event.get("timestamp")

        if ts:
            parsed = parse_timestamp(ts)
            if stats["first_ts"] is None:
                stats["first_ts"] = parsed
            stats["last_ts"] = parsed

        if et == "provider.request":
            stats["provider_requests"] += 1
            stats["models"].add(data.get("ModelName", ""))
        elif et == "provider.response":
            stats["provider_responses"] += 1
            content = data.get("ResponseContent", "")
            if "error" in content.lower()[:200]:
                stats["errors_detected"].append(
                    {
                        "source": "provider_response",
                        "preview": content[:200],
                    }
                )
        elif et == "tool":
            tool_name = data.get("tool_name", "unknown")
            if "result" in data:
                result = data.get("result", "")
                stats["tool_results"].append(
                    {
                        "tool": tool_name,
                        "result_preview": str(result)[:150],
                    }
                )
                if isinstance(result, str) and _is_real_error(result):
                    stats["errors_detected"].append(
                        {
                            "source": f"tool:{tool_name}",
                            "preview": result[:200],
                        }
                    )
            else:
                args = data.get("args", {})
                stats["tool_calls"].append(
                    {
                        "tool": tool_name,
                        "args": args,
                    }
                )
        elif et == "tool.reasoning":
            stats["reasoning_steps"].append(
                {
                    "tool": data.get("ToolName", ""),
                    "reasoning": data.get("ReasoningContent", "")[:200],
                }
            )

    stats["models"] = sorted(stats["models"])
    return stats


def extract_conversation_summary(session_data):
    if not session_data:
        return []

    messages = session_data.get("messages", [])
    summary = []

    for msg_wrapper in messages:
        msg = msg_wrapper.get("message", {})
        role = msg.get("Role", "")
        content = msg.get("Content", "")
        tool_calls = msg.get("ToolCalls") or []

        if role == "user" and content:
            summary.append(
                {
                    "role": "user",
                    "content": content[:300],
                }
            )
        elif role == "assistant" and content:
            summary.append(
                {
                    "role": "assistant",
                    "content": content[:300],
                    "tool_calls": len(tool_calls),
                }
            )

    return summary


def print_debug_report(
    session_id,
    meta,
    session_data,
    recording_stats,
    log_entries,
    children,
    include_logs,
):
    print(c("bold", "═" * 80))
    print(c("bold", f"  Debug Report: {session_id}"))
    print(c("bold", "═" * 80))
    print()

    print(c("bold", "  Session Metadata"))
    print(c("dim", "  " + "─" * 76))
    if meta:
        print(f"    Agent:      {c('magenta', meta.get('agent_id', 'N/A'))}")
        print(
            f"    Status:     {c('green' if meta.get('status') == 'completed' else 'red', meta.get('status', 'N/A'))}"
        )
        print(f"    Parent:     {c('cyan', meta.get('parent_id', 'N/A'))}")
        print(f"    Created:    {meta.get('created_at', 'N/A')}")
    else:
        print(f"    {c('yellow', 'No metadata file found')}")
    print()

    if children:
        print(c("bold", f"  Child Sessions ({len(children)})"))
        print(c("dim", "  " + "─" * 76))
        for child in children:
            cid = child.get("id", "")[:12]
            agent = child.get("agent_id", "")
            status = child.get("status", "")
            status_c = "green" if status == "completed" else "red"
            print(
                f"    {c('cyan', cid)} "
                f"agent={c('magenta', agent)} "
                f"status={c(status_c, status)}"
            )
        print()

    if recording_stats:
        print(c("bold", "  Recording Analysis"))
        print(c("dim", "  " + "─" * 76))
        rs = recording_stats
        duration = 0
        if rs["first_ts"] and rs["last_ts"]:
            duration = (rs["last_ts"] - rs["first_ts"]).total_seconds()
        print(f"    Events:          {rs['total_events']}")
        print(f"    Duration:        {duration:.1f}s")
        print(f"    LLM Requests:    {rs['provider_requests']}")
        print(f"    LLM Responses:   {rs['provider_responses']}")
        print(f"    Tool Calls:      {len(rs['tool_calls'])}")
        print(f"    Tool Results:    {len(rs['tool_results'])}")
        print(f"    Reasoning Steps: {len(rs['reasoning_steps'])}")
        print(f"    Models:          {', '.join(rs['models'])}")
        print()

        if rs["tool_calls"]:
            tool_freq = Counter(tc["tool"] for tc in rs["tool_calls"])
            print(c("bold", "    Tool Call Frequency:"))
            for tool, count in tool_freq.most_common():
                print(f"      {c('yellow', tool):>30}: {count}")
            print()

        if rs["reasoning_steps"]:
            print(c("bold", "    Agent Reasoning Chain:"))
            for i, step in enumerate(rs["reasoning_steps"], 1):
                print(
                    f"      {i}. [{c('yellow', step['tool'])}] "
                    f"{c('dim', step['reasoning'][:120])}"
                )
            print()

        if rs["errors_detected"]:
            print(c("bold", c("red", "    Errors Detected in Recording:")))
            for err in rs["errors_detected"]:
                print(f"      Source: {c('red', err['source'])}")
                print(f"      Detail: {c('dim', err['preview'][:150])}")
                print()
    else:
        print(c("yellow", "  No recording found for this session"))
        print()

    conversation = extract_conversation_summary(session_data)
    if conversation:
        print(c("bold", "  Conversation Summary"))
        print(c("dim", "  " + "─" * 76))
        for entry in conversation[:20]:
            role = entry["role"]
            content = entry["content"]
            if role == "user":
                print(f"    {c('cyan', 'User')}: {content[:150]}")
            else:
                tc = entry.get("tool_calls", 0)
                tc_str = f" [{tc} tools]" if tc else ""
                print(f"    {c('magenta', 'Agent')}{c('dim', tc_str)}: {content[:150]}")
        if len(conversation) > 20:
            print(f"    ... and {len(conversation) - 20} more exchanges")
        print()

    if include_logs and log_entries:
        errors = [e for e in log_entries if e["level"] in ("ERROR", "WARN")]
        infos = [e for e in log_entries if e["level"] == "INFO"]

        print(c("bold", f"  Correlated Log Entries ({len(log_entries)} in window)"))
        print(c("dim", "  " + "─" * 76))
        print(
            f"    {c('red', len(errors))} errors/warnings, "
            f"{c('green', len(infos))} info"
        )
        print()

        if errors:
            print(c("bold", "    Errors/Warnings During Session:"))
            for e in errors[:30]:
                ts = e["timestamp"].strftime("%H:%M:%S") if e["timestamp"] else "?"
                level_c = "red" if e["level"] == "ERROR" else "yellow"
                print(
                    f"      {c('dim', ts)} {c(level_c, e['level']):>16} {e['msg'][:80]}"
                )
            if len(errors) > 30:
                print(f"      ... and {len(errors) - 30} more")
            print()

    if not include_logs and log_entries:
        errors = [e for e in log_entries if e["level"] in ("ERROR", "WARN")]
        if errors:
            print(
                c(
                    "dim",
                    f"  Hint: {len(errors)} log errors/warnings in "
                    f"session window. Use --include-logs to see them.",
                )
            )
            print()


def find_error_sessions():
    error_sessions = []
    for path in SESSIONS_DIR.glob("*.meta.json"):
        try:
            with open(path) as f:
                meta = json.load(f)
        except (json.JSONDecodeError, OSError):
            continue

        session_id = meta.get("id", "")
        recording = load_recording(session_id)
        if not recording:
            continue

        for event in recording:
            et = event.get("event_type", "")
            data = event.get("data", {})
            if et == "tool":
                result = data.get("result", "")
                if isinstance(result, str) and _is_real_error(result):
                    error_sessions.append(
                        {
                            "session_id": session_id,
                            "agent_id": meta.get("agent_id", ""),
                            "status": meta.get("status", ""),
                            "error_source": data.get("tool_name", ""),
                            "error_preview": result[:150],
                        }
                    )
                    break

    return error_sessions


def main():
    parser = argparse.ArgumentParser(
        description="Cross-reference FlowState data for debugging"
    )
    parser.add_argument("session_id", nargs="?", help="Session ID to debug")
    parser.add_argument(
        "--parent",
        help="Show all sessions under a parent ID",
    )
    parser.add_argument(
        "--latest",
        action="store_true",
        help="Debug the most recent session",
    )
    parser.add_argument(
        "--errors",
        action="store_true",
        help="Find sessions with errors",
    )
    parser.add_argument(
        "--include-logs",
        action="store_true",
        help="Include correlated application log entries",
    )
    parser.add_argument(
        "--include-children",
        action="store_true",
        help="Also analyse child sessions",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="Output as JSON",
    )

    args = parser.parse_args()

    if args.errors:
        error_sessions = find_error_sessions()
        if args.json:
            print(json.dumps(error_sessions, indent=2))
        else:
            print(c("bold", f"Sessions with errors ({len(error_sessions)}):"))
            for es in error_sessions:
                print(
                    f"  {c('cyan', es['session_id'][:12])} "
                    f"agent={c('magenta', es['agent_id'])} "
                    f"status={es['status']} "
                    f"tool={c('yellow', es['error_source'])}"
                )
                print(f"    {c('red', es['error_preview'][:100])}")
        return

    session_id = args.session_id
    if args.latest:
        latest = find_latest_session()
        if not latest:
            print("No sessions found.", file=sys.stderr)
            sys.exit(1)
        session_id = latest["id"]

    if args.parent:
        children = find_children(args.parent)
        if not children:
            print(f"No child sessions found for {args.parent}", file=sys.stderr)
            sys.exit(1)

        print(c("bold", f"Parent chain: {args.parent}"))
        print(c("bold", f"Children: {len(children)}"))
        print()

        for child in children:
            cid = child["id"]
            print(c("bold", f"{'─' * 80}"))
            _debug_single(cid, args.include_logs)
        return

    if not session_id:
        parser.print_help()
        sys.exit(1)

    _debug_single(session_id, args.include_logs, args.include_children)


def _debug_single(session_id, include_logs=False, include_children=False):
    meta = load_session_meta(session_id)
    session_data = load_session_data(session_id)
    recording_events = load_recording(session_id)
    recording_stats = (
        analyse_recording_events(recording_events) if recording_events else None
    )

    log_entries = []
    if recording_stats and recording_stats["first_ts"] and recording_stats["last_ts"]:
        log_entries = load_logs_in_window(
            recording_stats["first_ts"],
            recording_stats["last_ts"],
        )

    children = []
    if include_children and meta:
        parent_id = meta.get("parent_id", "")
        if parent_id:
            children = find_children(parent_id)
            children = [ch for ch in children if ch["id"] != session_id]

    print_debug_report(
        session_id,
        meta,
        session_data,
        recording_stats,
        log_entries,
        children,
        include_logs,
    )


if __name__ == "__main__":
    main()
