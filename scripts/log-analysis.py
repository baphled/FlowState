#!/usr/bin/env python3
"""Parse and analyse FlowState application logs for patterns and anomalies.

Reads ~/.local/share/flowstate/flowstate.log (structured slog format)
and extracts error patterns, provider call performance, engine lifecycle
events, and recurring issues.

Usage:
    scripts/log-analysis.py
    scripts/log-analysis.py --errors-only
    scripts/log-analysis.py --since 2026-04-05
    scripts/log-analysis.py --provider-stats
    scripts/log-analysis.py --engine-lifecycle
    scripts/log-analysis.py --json
    scripts/log-analysis.py --tail 500
"""

import argparse
import json
import re
import sys
from collections import Counter, defaultdict
from datetime import datetime, date
from pathlib import Path

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

KV_RE = re.compile(r'(\w+)=(?:"([^"]*)"|(\S+))')


def c(colour, text):
    if not sys.stdout.isatty():
        return str(text)
    return f"{COLOURS.get(colour, '')}{text}{COLOURS['reset']}"


def parse_log_line(line):
    line = line.strip()
    match = LOG_LINE_RE.match(line)
    if not match:
        return None

    timestamp_str, level, msg, rest = match.groups()

    try:
        timestamp = datetime.fromisoformat(timestamp_str)
    except ValueError:
        timestamp = None

    fields = {}
    for kv_match in KV_RE.finditer(rest):
        key = kv_match.group(1)
        value = (
            kv_match.group(2) if kv_match.group(2) is not None else kv_match.group(3)
        )
        fields[key] = value

    return {
        "timestamp": timestamp,
        "level": level,
        "msg": msg,
        "fields": fields,
        "raw": line,
    }


def load_log(tail_lines=None, since=None):
    if not LOG_PATH.exists():
        print(f"Log file not found: {LOG_PATH}", file=sys.stderr)
        sys.exit(1)

    entries = []
    with open(LOG_PATH) as f:
        lines = f.readlines()

    if tail_lines:
        lines = lines[-tail_lines:]

    for line in lines:
        parsed = parse_log_line(line)
        if parsed is None:
            continue
        if since and parsed["timestamp"]:
            if parsed["timestamp"].date() < since:
                continue
        entries.append(parsed)

    return entries


def analyse_errors(entries):
    errors = [e for e in entries if e["level"] in ("ERROR", "WARN")]

    error_msgs = Counter()
    warn_msgs = Counter()
    error_timeline = defaultdict(list)

    for e in errors:
        msg = e["msg"]
        ts = e["timestamp"]

        if e["level"] == "ERROR":
            error_msgs[msg] += 1
        else:
            warn_msgs[msg] += 1

        if ts:
            date_key = ts.strftime("%Y-%m-%d %H:00")
            error_timeline[date_key].append(e["level"])

    return {
        "total_errors": sum(error_msgs.values()),
        "total_warnings": sum(warn_msgs.values()),
        "error_messages": error_msgs,
        "warning_messages": warn_msgs,
        "timeline": error_timeline,
    }


def analyse_provider_stats(entries):
    requests = [e for e in entries if e["msg"] == "engine stream request"]
    completions = [e for e in entries if e["msg"] == "tracer provider call complete"]

    provider_counts = Counter()
    model_counts = Counter()
    provider_model_counts = Counter()

    for req in requests:
        provider = req["fields"].get("provider", "unknown")
        model = req["fields"].get("model", "unknown")
        provider_counts[provider] += 1
        model_counts[model] += 1
        provider_model_counts[f"{provider}/{model}"] += 1

    return {
        "total_requests": len(requests),
        "total_completions": len(completions),
        "by_provider": dict(provider_counts.most_common()),
        "by_model": dict(model_counts.most_common()),
        "by_provider_model": dict(provider_model_counts.most_common()),
    }


def analyse_engine_lifecycle(entries):
    phases = [e for e in entries if e["msg"] == "harness phase detected"]
    tool_calls = [e for e in entries if e["msg"] == "engine tool call"]
    context_windows = [e for e in entries if e["msg"] == "engine context window"]
    hook_events = [e for e in entries if e["msg"].startswith("[hook]")]

    phase_counts = Counter()
    agent_phases = defaultdict(Counter)
    tool_names = Counter()
    context_sizes = []

    for p in phases:
        phase = p["fields"].get("phase", "unknown")
        agent = p["fields"].get("agentID", "unknown")
        phase_counts[phase] += 1
        agent_phases[agent][phase] += 1

    for tc in tool_calls:
        tool = tc["fields"].get("tool", "unknown")
        tool_names[tool] += 1

    for cw in context_windows:
        budget = cw["fields"].get("tokenBudget", "0")
        msgs = cw["fields"].get("messages", "0")
        try:
            context_sizes.append({"budget": int(budget), "messages": int(msgs)})
        except ValueError:
            pass

    msg_counts = []
    for h in hook_events:
        match = re.search(r"(\d+) messages", h["msg"])
        if match:
            msg_counts.append(int(match.group(1)))

    return {
        "harness_phases": dict(phase_counts.most_common()),
        "agent_phases": {a: dict(p.most_common()) for a, p in agent_phases.items()},
        "tool_calls": dict(tool_names.most_common()),
        "total_tool_calls": len(tool_calls),
        "context_windows": context_sizes[:10],
        "hook_message_counts": {
            "min": min(msg_counts) if msg_counts else 0,
            "max": max(msg_counts) if msg_counts else 0,
            "avg": sum(msg_counts) / len(msg_counts) if msg_counts else 0,
            "total_hooks": len(msg_counts),
        },
    }


def analyse_startup_patterns(entries):
    seeds = [e for e in entries if "agents seeded" in e["msg"]]
    discoveries = [
        e for e in entries if "discovered" in e["msg"] and "agent(s)" in e["msg"]
    ]
    mcp_failures = [
        e for e in entries if "MCP server" in e["msg"] and "failed" in e["msg"]
    ]
    plugin_warns = [e for e in entries if "discovering external plugins" in e["msg"]]

    startup_count = len(seeds)

    mcp_error_types = Counter()
    for m in mcp_failures:
        mcp_error_types[m["msg"][:80]] += 1

    return {
        "startup_count": startup_count,
        "agent_discoveries": len(discoveries),
        "mcp_failures": len(mcp_failures),
        "mcp_error_types": dict(mcp_error_types.most_common()),
        "plugin_warnings": len(plugin_warns),
    }


def print_error_report(error_analysis):
    print(c("bold", "═" * 72))
    print(c("bold", "  Error & Warning Analysis"))
    print(c("bold", "═" * 72))
    print()
    print(
        f"  Total: {c('red', error_analysis['total_errors'])} errors, "
        f"{c('yellow', error_analysis['total_warnings'])} warnings"
    )
    print()

    if error_analysis["error_messages"]:
        print(c("bold", "  Errors (by frequency):"))
        for msg, count in error_analysis["error_messages"].most_common(15):
            truncated = msg[:70] if len(msg) > 70 else msg
            print(f"    {c('red', f'{count:>4}')} × {truncated}")
        print()

    if error_analysis["warning_messages"]:
        print(c("bold", "  Warnings (by frequency):"))
        for msg, count in error_analysis["warning_messages"].most_common(15):
            truncated = msg[:70] if len(msg) > 70 else msg
            print(f"    {c('yellow', f'{count:>4}')} × {truncated}")
        print()

    if error_analysis["timeline"]:
        print(c("bold", "  Error/Warning Timeline (hourly):"))
        for hour, events in sorted(error_analysis["timeline"].items())[-24:]:
            errors = sum(1 for e in events if e == "ERROR")
            warns = sum(1 for e in events if e == "WARN")
            bar_e = c("red", "█" * min(errors, 30))
            bar_w = c("yellow", "▒" * min(warns, 30))
            print(f"    {c('dim', hour)} {bar_e}{bar_w} ({errors}E/{warns}W)")
        print()


def print_provider_report(provider_stats):
    print(c("bold", "─" * 72))
    print(c("bold", "  Provider & Model Statistics"))
    print(c("bold", "─" * 72))
    print()
    print(f"  Requests:    {provider_stats['total_requests']}")
    print(f"  Completions: {provider_stats['total_completions']}")
    print()

    print(c("bold", "  By Provider:"))
    for provider, count in provider_stats["by_provider"].items():
        bar = "█" * min(count // 5, 40)
        print(f"    {c('blue', provider):>23}: {count:>5} {c('dim', bar)}")
    print()

    print(c("bold", "  By Model:"))
    for model, count in provider_stats["by_model"].items():
        bar = "█" * min(count // 5, 40)
        print(f"    {c('cyan', model):>44}: {count:>5} {c('dim', bar)}")
    print()


def print_engine_report(engine_stats):
    print(c("bold", "─" * 72))
    print(c("bold", "  Engine Lifecycle"))
    print(c("bold", "─" * 72))
    print()

    if engine_stats["harness_phases"]:
        print(c("bold", "  Harness Phases:"))
        for phase, count in engine_stats["harness_phases"].items():
            print(f"    {c('magenta', phase):>23}: {count}")
        print()

    if engine_stats["agent_phases"]:
        print(c("bold", "  Agent Phase Breakdown:"))
        for agent, phases in engine_stats["agent_phases"].items():
            phase_str = ", ".join(f"{p}={n}" for p, n in phases.items())
            print(f"    {c('magenta', agent):>23}: {phase_str}")
        print()

    if engine_stats["tool_calls"]:
        print(
            c(
                "bold",
                f"  Engine Tool Calls ({engine_stats['total_tool_calls']} total):",
            )
        )
        for tool, count in engine_stats["tool_calls"].items():
            bar = "█" * min(count // 2, 40)
            print(f"    {c('yellow', tool):>23}: {count:>5} {c('dim', bar)}")
        print()

    hooks = engine_stats["hook_message_counts"]
    if hooks["total_hooks"]:
        print(c("bold", "  Context Window (hook message counts):"))
        print(f"    Min: {hooks['min']}, Max: {hooks['max']}, Avg: {hooks['avg']:.1f}")
        print(f"    Total hook activations: {hooks['total_hooks']}")
        print()


def print_startup_report(startup_stats):
    print(c("bold", "─" * 72))
    print(c("bold", "  Startup Patterns"))
    print(c("bold", "─" * 72))
    print()
    print(f"  Application starts: {startup_stats['startup_count']}")
    print(f"  Agent discoveries:  {startup_stats['agent_discoveries']}")
    print(f"  MCP failures:       {c('red', startup_stats['mcp_failures'])}")
    print(f"  Plugin warnings:    {c('yellow', startup_stats['plugin_warnings'])}")

    if startup_stats["mcp_error_types"]:
        print()
        print(c("bold", "  MCP Error Types:"))
        for msg, count in startup_stats["mcp_error_types"].items():
            print(f"    {c('red', f'{count:>3}')} × {msg}")
    print()


def main():
    parser = argparse.ArgumentParser(description="Analyse FlowState application logs")
    parser.add_argument(
        "--errors-only",
        action="store_true",
        help="Show only error/warning analysis",
    )
    parser.add_argument(
        "--provider-stats",
        action="store_true",
        help="Show provider/model statistics",
    )
    parser.add_argument(
        "--engine-lifecycle",
        action="store_true",
        help="Show engine lifecycle events",
    )
    parser.add_argument(
        "--since",
        help="Analyse logs since date (YYYY-MM-DD)",
    )
    parser.add_argument(
        "--tail",
        type=int,
        help="Only analyse last N lines",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="Output as JSON",
    )

    args = parser.parse_args()

    since = None
    if args.since:
        try:
            since = datetime.strptime(args.since, "%Y-%m-%d").date()
        except ValueError:
            print(f"Invalid date: {args.since}", file=sys.stderr)
            sys.exit(1)

    entries = load_log(tail_lines=args.tail, since=since)

    if not entries:
        print("No log entries found.")
        sys.exit(0)

    error_analysis = analyse_errors(entries)
    provider_stats = analyse_provider_stats(entries)
    engine_stats = analyse_engine_lifecycle(entries)
    startup_stats = analyse_startup_patterns(entries)

    if args.json:
        output = {
            "total_entries": len(entries),
            "errors": {
                "total_errors": error_analysis["total_errors"],
                "total_warnings": error_analysis["total_warnings"],
                "top_errors": dict(error_analysis["error_messages"].most_common(20)),
                "top_warnings": dict(
                    error_analysis["warning_messages"].most_common(20)
                ),
            },
            "providers": provider_stats,
            "engine": engine_stats,
            "startup": startup_stats,
        }
        print(json.dumps(output, indent=2, default=str))
        return

    first_ts = next((e["timestamp"] for e in entries if e["timestamp"]), None)
    last_ts = next((e["timestamp"] for e in reversed(entries) if e["timestamp"]), None)

    print(c("bold", "═" * 72))
    print(c("bold", "  FlowState Log Analysis"))
    print(c("bold", "═" * 72))
    print(f"  Log file:  {LOG_PATH}")
    print(f"  Entries:   {len(entries)}")
    if first_ts:
        print(f"  From:      {first_ts.isoformat()}")
    if last_ts:
        print(f"  To:        {last_ts.isoformat()}")
    print()

    if args.errors_only:
        print_error_report(error_analysis)
        return

    if args.provider_stats:
        print_provider_report(provider_stats)
        return

    if args.engine_lifecycle:
        print_engine_report(engine_stats)
        return

    print_error_report(error_analysis)
    print_provider_report(provider_stats)
    print_engine_report(engine_stats)
    print_startup_report(startup_stats)


if __name__ == "__main__":
    main()
