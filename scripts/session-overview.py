#!/usr/bin/env python3
"""Aggregate overview of all FlowState sessions with statistics.

Reads session metadata, session data, and recording files to produce
a comprehensive overview of all sessions, agent activity, tool usage
patterns, and session health.

Usage:
    scripts/session-overview.py
    scripts/session-overview.py --json
    scripts/session-overview.py --agent librarian
    scripts/session-overview.py --since 2026-04-05
    scripts/session-overview.py --top-tools
    scripts/session-overview.py --parent session-1775310002435946150
"""

import argparse
import json
import sys
from collections import Counter, defaultdict
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
}


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


def load_all_meta():
    metas = []
    for path in SESSIONS_DIR.glob("*.meta.json"):
        try:
            with open(path) as f:
                metas.append(json.load(f))
        except (json.JSONDecodeError, OSError):
            continue
    return metas


def load_session_message_count(session_id):
    path = SESSIONS_DIR / f"{session_id}.json"
    if not path.exists():
        return 0
    try:
        with open(path) as f:
            data = json.load(f)
            return len(data.get("messages", []))
    except (json.JSONDecodeError, OSError):
        return 0


def scan_recording(session_id):
    path = RECORDINGS_DIR / f"{session_id}.jsonl"
    if not path.exists():
        return None

    stats = {
        "events": 0,
        "provider_requests": 0,
        "provider_responses": 0,
        "tool_calls": 0,
        "tool_results": 0,
        "tools_used": Counter(),
        "models": set(),
        "duration_seconds": 0,
        "first_ts": None,
        "last_ts": None,
    }

    try:
        with open(path) as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    event = json.loads(line)
                except json.JSONDecodeError:
                    continue

                stats["events"] += 1
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
                elif et == "tool":
                    tool_name = data.get("tool_name", "unknown")
                    if "result" in data:
                        stats["tool_results"] += 1
                    else:
                        stats["tool_calls"] += 1
                        stats["tools_used"][tool_name] += 1
    except OSError:
        return None

    if stats["first_ts"] and stats["last_ts"]:
        stats["duration_seconds"] = (
            stats["last_ts"] - stats["first_ts"]
        ).total_seconds()

    stats["models"] = sorted(stats["models"])
    return stats


def format_duration(seconds):
    if seconds < 60:
        return f"{seconds:.0f}s"
    if seconds < 3600:
        return f"{seconds / 60:.1f}m"
    return f"{seconds / 3600:.1f}h"


def build_session_rows(metas, since=None, agent_filter=None, parent_filter=None):
    rows = []
    for meta in metas:
        session_id = meta.get("id", "")
        agent_id = meta.get("agent_id", "")
        status = meta.get("status", "unknown")
        created = meta.get("created_at", "")
        parent = meta.get("parent_id", "")

        if agent_filter and agent_id != agent_filter:
            continue
        if parent_filter and parent != parent_filter:
            continue

        if since and created:
            try:
                ts = parse_timestamp(created)
                if ts.date() < since:
                    continue
            except (ValueError, TypeError):
                pass

        msg_count = load_session_message_count(session_id)
        recording = scan_recording(session_id)

        row = {
            "session_id": session_id,
            "agent_id": agent_id,
            "status": status,
            "parent_id": parent,
            "created_at": created,
            "messages": msg_count,
            "recording": recording,
        }
        rows.append(row)

    rows.sort(key=lambda r: r.get("created_at", ""), reverse=True)
    return rows


def print_session_table(rows):
    print(c("bold", "═" * 110))
    print(c("bold", "  FlowState Session Overview"))
    print(c("bold", "═" * 110))
    print()

    header = (
        f"  {'ID':>12}  {'Agent':>12}  {'Status':>10}  "
        f"{'Msgs':>5}  {'Events':>6}  {'LLM':>4}  "
        f"{'Tools':>5}  {'Duration':>8}  {'Created':>20}"
    )
    print(c("dim", header))
    print(c("dim", "  " + "─" * 106))

    for row in rows:
        sid = row["session_id"][:12]
        agent = row["agent_id"] or "—"
        status = row["status"]
        msgs = row["messages"]
        rec = row["recording"]

        status_colour = "green" if status == "completed" else "yellow"
        if status == "failed":
            status_colour = "red"

        events = rec["events"] if rec else 0
        llm = rec["provider_requests"] if rec else 0
        tools = rec["tool_calls"] if rec else 0
        duration = format_duration(rec["duration_seconds"]) if rec else "—"

        created = ""
        if row["created_at"]:
            try:
                ts = parse_timestamp(row["created_at"])
                created = ts.strftime("%Y-%m-%d %H:%M")
            except (ValueError, TypeError):
                created = row["created_at"][:16]

        print(
            f"  {c('cyan', sid):>23}  {c('magenta', agent):>23}  "
            f"{c(status_colour, status):>21}  "
            f"{msgs:>5}  {events:>6}  {llm:>4}  "
            f"{tools:>5}  {duration:>8}  {created:>20}"
        )

    print()


def print_aggregate_stats(rows):
    total = len(rows)
    completed = sum(1 for r in rows if r["status"] == "completed")
    failed = sum(1 for r in rows if r["status"] == "failed")
    other = total - completed - failed

    agent_counts = Counter(r["agent_id"] for r in rows if r["agent_id"])
    tool_counts = Counter()
    model_counts = Counter()
    total_events = 0
    total_llm = 0
    total_tool_calls = 0
    total_duration = 0

    for row in rows:
        rec = row.get("recording")
        if not rec:
            continue
        total_events += rec["events"]
        total_llm += rec["provider_requests"]
        total_tool_calls += rec["tool_calls"]
        total_duration += rec["duration_seconds"]
        tool_counts.update(rec["tools_used"])
        for m in rec["models"]:
            model_counts[m] += 1

    parent_groups = defaultdict(list)
    for r in rows:
        pid = r.get("parent_id", "")
        if pid:
            parent_groups[pid].append(r)

    print(c("bold", "─" * 110))
    print(c("bold", "  Aggregate Statistics"))
    print(c("bold", "─" * 110))
    print()
    print(f"  Sessions:    {c('bold', total)} total")
    print(
        f"               {c('green', completed)} completed, "
        f"{c('red', failed)} failed, "
        f"{c('yellow', other)} other"
    )
    print()
    print(f"  Events:      {total_events} total across all recordings")
    print(f"  LLM Calls:   {total_llm} provider requests")
    print(f"  Tool Calls:  {total_tool_calls} invocations")
    print(f"  Duration:    {format_duration(total_duration)} total compute")
    print()

    print(f"  {c('bold', 'Agent Distribution:')}")
    for agent, count in agent_counts.most_common():
        bar = "█" * min(count, 40)
        print(f"    {c('magenta', agent):>23}: {count:>3} {c('dim', bar)}")

    print()
    print(f"  {c('bold', 'Tool Usage (top 10):')}")
    for tool, count in tool_counts.most_common(10):
        bar = "█" * min(count, 40)
        print(f"    {c('yellow', tool):>23}: {count:>3} {c('dim', bar)}")

    print()
    print(f"  {c('bold', 'Models Used:')}")
    for model, count in model_counts.most_common():
        print(f"    {c('blue', model):>23}: {count:>3} sessions")

    if parent_groups:
        print()
        print(f"  {c('bold', 'Delegation Chains (parent → children):')}")
        for parent, children in sorted(
            parent_groups.items(),
            key=lambda x: len(x[1]),
            reverse=True,
        )[:10]:
            agents = [ch["agent_id"] for ch in children]
            agent_str = ", ".join(agents)
            print(
                f"    {c('cyan', parent[:30]):>41} → "
                f"{len(children)} children ({agent_str})"
            )

    print()


def print_top_tools(rows):
    tool_counts = Counter()
    tool_per_agent = defaultdict(Counter)

    for row in rows:
        rec = row.get("recording")
        if not rec:
            continue
        agent = row.get("agent_id", "unknown")
        tool_counts.update(rec["tools_used"])
        tool_per_agent[agent].update(rec["tools_used"])

    print(c("bold", "═" * 72))
    print(c("bold", "  Tool Usage Breakdown"))
    print(c("bold", "═" * 72))
    print()

    for tool, count in tool_counts.most_common():
        print(f"  {c('yellow', tool)}: {count} calls")
        for agent, agent_tools in sorted(tool_per_agent.items()):
            if tool in agent_tools:
                print(f"    └─ {c('magenta', agent)}: {agent_tools[tool]}")
        print()


def main():
    parser = argparse.ArgumentParser(
        description="Aggregate overview of all FlowState sessions"
    )
    parser.add_argument("--json", action="store_true", help="Output as JSON")
    parser.add_argument("--agent", help="Filter by agent ID")
    parser.add_argument(
        "--since",
        help="Show sessions since date (YYYY-MM-DD)",
    )
    parser.add_argument("--parent", help="Filter by parent session ID")
    parser.add_argument(
        "--top-tools",
        action="store_true",
        help="Show detailed tool usage breakdown",
    )

    args = parser.parse_args()

    since = None
    if args.since:
        try:
            since = datetime.strptime(args.since, "%Y-%m-%d").date()
        except ValueError:
            print(f"Invalid date format: {args.since}", file=sys.stderr)
            sys.exit(1)

    metas = load_all_meta()
    rows = build_session_rows(
        metas,
        since=since,
        agent_filter=args.agent,
        parent_filter=args.parent,
    )

    if args.json:
        output = []
        for row in rows:
            rec = row.get("recording")
            entry = {
                "session_id": row["session_id"],
                "agent_id": row["agent_id"],
                "status": row["status"],
                "parent_id": row["parent_id"],
                "created_at": row["created_at"],
                "messages": row["messages"],
            }
            if rec:
                entry["events"] = rec["events"]
                entry["provider_requests"] = rec["provider_requests"]
                entry["tool_calls"] = rec["tool_calls"]
                entry["duration_seconds"] = rec["duration_seconds"]
                entry["tools_used"] = dict(rec["tools_used"])
                entry["models"] = rec["models"]
            output.append(entry)
        print(json.dumps(output, indent=2, default=str))
        return

    if not rows:
        print("No sessions found matching criteria.")
        sys.exit(0)

    if args.top_tools:
        print_top_tools(rows)
        return

    print_session_table(rows)
    print_aggregate_stats(rows)


if __name__ == "__main__":
    main()
