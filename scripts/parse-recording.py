#!/usr/bin/env python3
"""Parse FlowState session recordings (JSONL) into human-readable timelines.

Reads session recording files from ~/.cache/flowstate/session-recordings/
and produces a structured timeline showing agent actions, tool calls,
provider requests/responses, and timing information.

Usage:
    scripts/parse-recording.py <session-id>
    scripts/parse-recording.py <session-id> --json
    scripts/parse-recording.py <session-id> --tools-only
    scripts/parse-recording.py <session-id> --verbose
"""

import argparse
import json
import sys
from datetime import datetime
from pathlib import Path

RECORDINGS_DIR = Path.home() / ".cache" / "flowstate" / "session-recordings"
SESSIONS_DIR = Path.home() / ".local" / "share" / "flowstate" / "sessions"

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
    "grey": "\033[90m",
}

EVENT_ICONS = {
    "session": "🔵",
    "provider.request": "📤",
    "provider.request.retry": "🔁",
    "provider.response": "📥",
    "tool": "🔧",
    "tool.reasoning": "💭",
    "tool.execute.result": "✅",
    "tool.execute.error": "❌",
}


def c(colour, text):
    if not sys.stdout.isatty():
        return text
    return f"{COLOURS.get(colour, '')}{text}{COLOURS['reset']}"


def parse_timestamp(ts):
    for fmt in ("%Y-%m-%dT%H:%M:%S.%f%z", "%Y-%m-%dT%H:%M:%S%z"):
        try:
            return datetime.fromisoformat(ts)
        except ValueError:
            continue
    clean = ts[:26] + ts[-6:]
    return datetime.fromisoformat(clean)


def load_recording(session_id):
    path = RECORDINGS_DIR / f"{session_id}.jsonl"
    if not path.exists():
        print(f"Recording not found: {path}", file=sys.stderr)
        sys.exit(1)

    events = []
    with open(path) as f:
        for line_num, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError as e:
                print(f"Warning: bad JSON on line {line_num}: {e}", file=sys.stderr)
    return events


def load_session_meta(session_id):
    meta_path = SESSIONS_DIR / f"{session_id}.meta.json"
    if meta_path.exists():
        with open(meta_path) as f:
            return json.load(f)
    return None


def format_duration(ms):
    if ms is None or ms == 0:
        return "N/A"
    if ms < 1000:
        return f"{ms}ms"
    seconds = ms / 1000
    if seconds < 60:
        return f"{seconds:.1f}s"
    minutes = seconds / 60
    return f"{minutes:.1f}m"


def truncate(text, max_len=120):
    if not text:
        return ""
    text = text.replace("\n", " ").strip()
    if len(text) <= max_len:
        return text
    return text[: max_len - 3] + "..."


def render_session_event(event, data):
    action = data.get("Action", "unknown")
    session_id = data.get("SessionID", "")
    return f"Session {c('cyan', session_id[:12])} → {c('bold', action)}"


def render_provider_request(event, data, verbose):
    model = data.get("ModelName", "unknown")
    agent = data.get("AgentID", "unknown")
    req = data.get("Request", {})
    msgs = req.get("Messages", [])
    tools = req.get("Tools", [])

    line = (
        f"Agent {c('magenta', agent)} → "
        f"{c('blue', model)} "
        f"({len(msgs)} messages, {len(tools)} tools)"
    )

    if verbose and msgs:
        last_msg = msgs[-1]
        role = last_msg.get("Role", "")
        content = last_msg.get("Content", "")
        line += f"\n    Last msg ({role}): {truncate(content, 200)}"

    return line


def render_provider_response(event, data, verbose):
    model = data.get("ModelName", "unknown")
    agent = data.get("AgentID", "unknown")
    duration = data.get("DurationMS")
    tool_calls = data.get("ToolCalls") or []
    content = data.get("ResponseContent", "")

    dur_str = format_duration(duration)
    line = f"Agent {c('magenta', agent)} ← {c('blue', model)} [{c('yellow', dur_str)}]"

    if tool_calls:
        names = [tc.get("Name", "?") for tc in tool_calls]
        line += f" → tools: {', '.join(names)}"
    elif content:
        line += f" → text ({len(content)} chars)"

    if verbose and content:
        line += f"\n    Response: {truncate(content, 300)}"

    return line


def render_tool_event(event, data, verbose):
    name = data.get("tool_name", "unknown")
    args = data.get("args", {})
    result = data.get("result")
    has_result = "result" in data

    if has_result:
        result_preview = truncate(str(result), 100) if result else "(empty)"
        line = f"{c('green', name)} ✓ result: {c('dim', result_preview)}"
    else:
        args_preview = ""
        if args:
            if "command" in args:
                args_preview = truncate(args["command"], 100)
            elif "path" in args:
                args_preview = args["path"]
            elif "key" in args:
                op = args.get("operation", "")
                args_preview = f"{op} → {args['key']}"
            else:
                args_preview = truncate(json.dumps(args), 100)
        line = f"{c('yellow', name)} → {args_preview}"

    return line


def render_tool_reasoning(event, data):
    tool = data.get("ToolName", "unknown")
    reason = data.get("ReasoningContent", "")
    return f"Before {c('yellow', tool)}: {c('dim', truncate(reason, 150))}"


def render_timeline(events, verbose=False, tools_only=False):
    if not events:
        print("No events found.")
        return

    first_ts = parse_timestamp(events[0].get("timestamp", ""))
    lines = []

    for event in events:
        et = event.get("event_type", "unknown")
        data = event.get("data", {})
        seq = event.get("seq", 0)
        ts = event.get("timestamp", "")

        if tools_only and et not in ("tool", "tool.reasoning"):
            continue

        parsed_ts = parse_timestamp(ts)
        elapsed = (parsed_ts - first_ts).total_seconds()
        icon = EVENT_ICONS.get(et, "❓")
        time_str = f"+{elapsed:6.1f}s"

        if et == "session":
            detail = render_session_event(event, data)
        elif et == "provider.request":
            detail = render_provider_request(event, data, verbose)
        elif et == "provider.response":
            detail = render_provider_response(event, data, verbose)
        elif et == "tool":
            detail = render_tool_event(event, data, verbose)
        elif et == "tool.reasoning":
            detail = render_tool_reasoning(event, data)
        elif et == "tool.execute.result":
            tool_name = data.get("tool_name", "unknown")
            result = data.get("result", "")
            preview = result[:120] + "…" if len(result) > 120 else result
            detail = f"{c('green', tool_name)} → {c('dim', preview)}"
        elif et == "tool.execute.error":
            tool_name = data.get("tool_name", "unknown")
            error = data.get("error", "unknown error")
            detail = f"{c('red', tool_name)} ERROR: {error}"
        elif et == "provider.request.retry":
            reason = data.get("reason", "")
            attempt = data.get("attempt", "")
            detail = f"retry attempt={attempt} reason={reason}"
        else:
            detail = f"Unknown event: {et}"

        lines.append(
            f"  {c('grey', time_str)} {icon} {c('dim', f'[{seq:3d}]')} {detail}"
        )

    return "\n".join(lines)


def compute_stats(events):
    stats = {
        "total_events": len(events),
        "provider_requests": 0,
        "provider_responses": 0,
        "tool_calls": 0,
        "tool_results": 0,
        "unique_tools": set(),
        "models_used": set(),
        "agents": set(),
        "reasoning_steps": 0,
        "total_response_chars": 0,
    }

    for event in events:
        et = event.get("event_type", "")
        data = event.get("data", {})

        if et == "provider.request":
            stats["provider_requests"] += 1
            stats["models_used"].add(data.get("ModelName", ""))
            stats["agents"].add(data.get("AgentID", ""))
        elif et == "provider.response":
            stats["provider_responses"] += 1
            content = data.get("ResponseContent", "")
            stats["total_response_chars"] += len(content)
        elif et == "tool":
            if "result" in data:
                stats["tool_results"] += 1
            else:
                stats["tool_calls"] += 1
            stats["unique_tools"].add(data.get("tool_name", ""))
        elif et == "tool.execute.result":
            stats["tool_results"] += 1
            stats["unique_tools"].add(data.get("tool_name", ""))
        elif et == "tool.execute.error":
            stats["unique_tools"].add(data.get("tool_name", ""))
        elif et == "tool.reasoning":
            stats["reasoning_steps"] += 1

    if events:
        first = parse_timestamp(events[0]["timestamp"])
        last = parse_timestamp(events[-1]["timestamp"])
        stats["duration_seconds"] = (last - first).total_seconds()
        stats["start_time"] = first.isoformat()
        stats["end_time"] = last.isoformat()
    else:
        stats["duration_seconds"] = 0

    stats["unique_tools"] = sorted(stats["unique_tools"])
    stats["models_used"] = sorted(stats["models_used"])
    stats["agents"] = sorted(stats["agents"])

    return stats


def print_header(session_id, meta, stats):
    print(c("bold", "═" * 72))
    print(c("bold", f"  Session Recording: {session_id}"))
    print(c("bold", "═" * 72))

    if meta:
        print(f"  Agent:    {c('magenta', meta.get('agent_id', 'N/A'))}")
        print(f"  Status:   {c('green', meta.get('status', 'N/A'))}")
        parent = meta.get("parent_id", "")
        if parent:
            print(f"  Parent:   {c('cyan', parent)}")

    print(f"  Events:   {stats['total_events']}")
    dur = f"{stats['duration_seconds']:.1f}s"
    print(f"  Duration: {c('yellow', dur)}")
    print(
        f"  LLM Calls: {stats['provider_requests']} requests, "
        f"{stats['provider_responses']} responses"
    )
    print(
        f"  Tools:    {stats['tool_calls']} calls → "
        f"{stats['tool_results']} results "
        f"({', '.join(stats['unique_tools'])})"
    )
    print(f"  Models:   {', '.join(stats['models_used'])}")
    print(f"  Agents:   {', '.join(stats['agents'])}")
    if stats.get("start_time"):
        print(f"  Start:    {stats['start_time']}")
    print(c("bold", "─" * 72))


def main():
    parser = argparse.ArgumentParser(
        description="Parse FlowState session recordings into readable timelines"
    )
    parser.add_argument("session_id", help="Session ID (UUID) to parse")
    parser.add_argument("--json", action="store_true", help="Output stats as JSON")
    parser.add_argument(
        "--tools-only",
        action="store_true",
        help="Show only tool calls and reasoning",
    )
    parser.add_argument(
        "--verbose", "-v", action="store_true", help="Show full message content"
    )
    parser.add_argument(
        "--stats-only",
        action="store_true",
        help="Show only statistics, no timeline",
    )

    args = parser.parse_args()

    events = load_recording(args.session_id)
    meta = load_session_meta(args.session_id)
    stats = compute_stats(events)

    if args.json:
        print(json.dumps(stats, indent=2, default=str))
        return

    print_header(args.session_id, meta, stats)

    if not args.stats_only:
        print()
        timeline = render_timeline(
            events, verbose=args.verbose, tools_only=args.tools_only
        )
        print(timeline)
        print()
        print(c("bold", "─" * 72))


if __name__ == "__main__":
    main()
