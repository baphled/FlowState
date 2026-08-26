#!/usr/bin/env python3
"""Display the conversation history of a FlowState session.

Renders the message-level history from session data files, showing
user prompts, assistant responses, tool calls/results, errors, and
all IDs (session, message, tool-call, task, parent) in a format
suitable for transcripts and debugging.

Short mode:  one line per message — role, ID, tool names, content preview.
Detail mode: full content, tool arguments, results, and extracted IDs.

Usage:
    scripts/session-history.py <session-id>
    scripts/session-history.py <session-id> --detail
    scripts/session-history.py <session-id> --json
    scripts/session-history.py <session-id> --ids-only
    scripts/session-history.py --latest
    scripts/session-history.py --latest --detail
"""

import argparse
import json
import re
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
    "white": "\033[37m",
}

ROLE_COLOURS = {
    "user": "cyan",
    "assistant": "magenta",
    "tool": "yellow",
    "system": "grey",
}

ROLE_ICONS = {
    "user": "👤",
    "assistant": "🤖",
    "tool": "🔧",
    "system": "⚙️ ",
}

UUID_RE = re.compile(
    r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}",
    re.IGNORECASE,
)
TASK_ID_RE = re.compile(r"task[_-]id[\"']?\s*[:=]\s*[\"']?([0-9a-f-]{36})", re.I)
SESSION_ID_RE = re.compile(
    r"session[_-]?(?:id|[0-9])[\"']?\s*[:=]?\s*[\"']?([0-9a-f-]{20,})", re.I
)


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
    if not path.exists():
        return None
    with open(path) as f:
        return json.load(f)


def load_recording_timestamps(session_id):
    path = RECORDINGS_DIR / f"{session_id}.jsonl"
    if not path.exists():
        return None, None
    first_ts = last_ts = None
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                ev = json.loads(line)
                ts = ev.get("timestamp")
                if ts:
                    parsed = parse_timestamp(ts)
                    if first_ts is None:
                        first_ts = parsed
                    last_ts = parsed
            except (json.JSONDecodeError, ValueError):
                continue
    return first_ts, last_ts


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


def extract_ids_from_text(text):
    ids = {}
    task_matches = TASK_ID_RE.findall(text)
    if task_matches:
        ids["task_ids"] = list(set(task_matches))
    session_matches = SESSION_ID_RE.findall(text)
    if session_matches:
        ids["session_refs"] = list(set(session_matches))
    return ids


def extract_ids_from_args(args):
    ids = {}
    if not args:
        return ids
    for key in ("task_id", "session_id", "id"):
        val = args.get(key)
        if val:
            ids[key] = val
    chain_id = (args.get("handoff") or {}).get("chainID")
    if chain_id:
        ids["chain_id"] = chain_id
    return ids


def detect_errors_in_content(content):
    if not content:
        return []
    errors = []
    for line in content.split("\n"):
        low = line.lower().strip()
        if any(
            pat in low
            for pat in (
                "error:",
                "failed:",
                "fatal:",
                "panic:",
                "permission denied",
                "no such file",
                "command not found",
                "exit status",
                "connection refused",
                "timed out",
            )
        ):
            errors.append(line.strip())
    return errors


def build_history(session_data):
    if not session_data:
        return []

    messages = session_data.get("messages", [])
    history = []
    tool_call_map = {}

    for msg_wrapper in messages:
        msg = msg_wrapper.get("message", {})
        role = msg.get("Role", "unknown")
        content = msg.get("Content", "")
        tool_calls = msg.get("ToolCalls") or []
        model_id = msg.get("ModelID", "")
        msg_id = msg_wrapper.get("id", "")
        embedded = msg_wrapper.get("embedded", False)

        entry = {
            "msg_id": msg_id,
            "role": role,
            "content": content,
            "model_id": model_id,
            "embedded": embedded,
            "tool_calls": [],
            "tool_result_for": None,
            "errors": detect_errors_in_content(content),
            "extracted_ids": extract_ids_from_text(content),
        }

        if role == "assistant" and tool_calls:
            for tc in tool_calls:
                tc_id = tc.get("ID", "")
                tc_name = tc.get("Name", "")
                tc_args = tc.get("Arguments") or {}
                call_entry = {
                    "call_id": tc_id,
                    "name": tc_name,
                    "args": tc_args,
                    "extracted_ids": extract_ids_from_args(tc_args),
                }
                entry["tool_calls"].append(call_entry)
                tool_call_map[tc_id] = tc_name

        if role == "tool" and tool_calls:
            tc = tool_calls[0]
            tc_id = tc.get("ID", "")
            entry["tool_result_for"] = {
                "call_id": tc_id,
                "tool_name": tool_call_map.get(tc_id, "unknown"),
            }

        history.append(entry)

    return history


def truncate(text, max_len=120):
    if not text:
        return ""
    text = text.replace("\n", " ").strip()
    if len(text) <= max_len:
        return text
    return text[: max_len - 3] + "..."


def print_header(session_id, meta, history, start_ts, end_ts):
    print(c("bold", "═" * 80))
    print(c("bold", "  Session History"))
    print(c("bold", "═" * 80))
    print(f"  Session ID:  {c('cyan', session_id)}")

    if meta:
        print(f"  Agent:       {c('magenta', meta.get('agent_id', '—'))}")
        status = meta.get("status", "—")
        sc = (
            "green"
            if status == "completed"
            else "red"
            if status == "failed"
            else "yellow"
        )
        print(f"  Status:      {c(sc, status)}")
        parent = meta.get("parent_id", "")
        if parent:
            print(f"  Parent ID:   {c('cyan', parent)}")
        print(f"  Created:     {meta.get('created_at', '—')}")

    total = len(history)
    users = sum(1 for h in history if h["role"] == "user")
    assistants = sum(1 for h in history if h["role"] == "assistant")
    tools = sum(1 for h in history if h["role"] == "tool")
    errors = sum(1 for h in history if h["errors"])

    print(
        f"  Messages:    {total} ({users} user, {assistants} assistant, {tools} tool)"
    )
    if errors:
        print(f"  Errors:      {c('red', str(errors))} messages contain errors")
    if start_ts and end_ts:
        dur = (end_ts - start_ts).total_seconds()
        dur_str = f"{dur:.0f}s" if dur < 60 else f"{dur / 60:.1f}m"
        print(f"  Duration:    {c('yellow', dur_str)}")

    print(c("bold", "─" * 80))


def print_short(history):
    print()
    for i, entry in enumerate(history):
        role = entry["role"]
        msg_id = entry["msg_id"][:8]
        icon = ROLE_ICONS.get(role, "?")
        role_c = ROLE_COLOURS.get(role, "white")
        content = entry["content"]
        tool_calls = entry["tool_calls"]
        result_for = entry["tool_result_for"]
        errors = entry["errors"]
        has_error = bool(errors)

        parts = [f"  {c('dim', f'{i:>3}')}"]
        parts.append(f"{icon}")
        parts.append(f"{c('dim', msg_id)}")
        parts.append(f"{c(role_c, role):>21}")

        if role == "assistant" and tool_calls:
            names = [tc["name"] for tc in tool_calls]
            ids_found = {}
            for tc in tool_calls:
                ids_found.update(tc.get("extracted_ids", {}))
            parts.append(f"→ {c('yellow', ', '.join(names))}")
            if ids_found:
                id_strs = [f"{k}={v}" for k, v in ids_found.items()]
                parts.append(c("dim", f"[{', '.join(id_strs)}]"))
            if content:
                parts.append(c("dim", truncate(content, 60)))
        elif role == "tool":
            tool_name = result_for["tool_name"] if result_for else "?"
            call_id = result_for["call_id"][:12] if result_for else "?"
            result_len = len(content)
            error_mark = c("red", " ERR") if has_error else ""
            parts.append(
                f"← {c('green', tool_name)} "
                f"{c('dim', f'({result_len} chars)')}"
                f"{error_mark}"
            )
        elif role == "user":
            parts.append(truncate(content, 80))
        else:
            parts.append(truncate(content, 80))

        print(" ".join(parts))

    print()


def print_detail(history):
    print()
    for i, entry in enumerate(history):
        role = entry["role"]
        msg_id = entry["msg_id"]
        icon = ROLE_ICONS.get(role, "?")
        role_c = ROLE_COLOURS.get(role, "white")
        content = entry["content"]
        tool_calls = entry["tool_calls"]
        result_for = entry["tool_result_for"]
        errors = entry["errors"]
        extracted_ids = entry["extracted_ids"]
        embedded = entry["embedded"]

        print(c("dim", f"  {'─' * 76}"))
        header = f"  {icon} {c(role_c, f'[{role}]')}  msg_id={c('dim', msg_id)}"
        if embedded:
            header += c("dim", " (embedded)")
        if entry.get("model_id"):
            header += f"  model={c('blue', entry['model_id'])}"
        print(header)

        if role == "assistant" and tool_calls:
            for tc in tool_calls:
                print(
                    f"    ┌─ {c('yellow', tc['name'])}  call_id={c('dim', tc['call_id'])}"
                )
                tc_ids = tc.get("extracted_ids", {})
                if tc_ids:
                    for k, v in tc_ids.items():
                        print(f"    │  {c('cyan', k)}: {v}")
                args = tc.get("args", {})
                if args:
                    if "command" in args:
                        print(f"    │  $ {args['command']}")
                    elif "path" in args:
                        print(f"    │  path: {args['path']}")
                    elif "message" in args:
                        msg_preview = truncate(args["message"], 200)
                        print(f"    │  message: {msg_preview}")
                    else:
                        for k, v in args.items():
                            v_str = str(v)
                            if len(v_str) > 120:
                                v_str = v_str[:117] + "..."
                            print(f"    │  {k}: {v_str}")
                print(f"    └─")

        if role == "tool" and result_for:
            tool_name = result_for["tool_name"]
            call_id = result_for["call_id"]
            print(
                f"    result for {c('yellow', tool_name)} call_id={c('dim', call_id)}"
            )

        if content:
            if role == "tool":
                lines = content.split("\n")
                if len(lines) <= 15:
                    for line in lines:
                        print(f"    {c('dim', line)}")
                else:
                    for line in lines[:8]:
                        print(f"    {c('dim', line)}")
                    print(
                        f"    {c('dim', f'... ({len(lines) - 13} lines omitted) ...')}"
                    )
                    for line in lines[-5:]:
                        print(f"    {c('dim', line)}")
            elif role == "user":
                for line in content.split("\n"):
                    print(f"    {line}")
            else:
                lines = content.split("\n")
                if len(lines) <= 30:
                    for line in lines:
                        print(f"    {line}")
                else:
                    for line in lines[:20]:
                        print(f"    {line}")
                    remaining = len(lines) - 25
                    print(f"    {c('dim', f'... ({remaining} lines omitted) ...')}")
                    for line in lines[-5:]:
                        print(f"    {line}")

        if errors:
            print(f"    {c('red', '⚠ Errors detected:')}")
            for err in errors[:5]:
                print(f"      {c('red', err[:120])}")
            if len(errors) > 5:
                print(f"      {c('red', f'... and {len(errors) - 5} more')}")

        if extracted_ids:
            parts = [f"{k}={v}" for k, v in extracted_ids.items()]
            print(f"    {c('cyan', 'IDs found:')} {', '.join(parts)}")

    print()
    print(c("dim", f"  {'─' * 76}"))


def collect_all_ids(session_id, meta, history):
    ids = {"session_id": session_id}

    if meta:
        if meta.get("parent_id"):
            ids["parent_id"] = meta["parent_id"]
        if meta.get("agent_id"):
            ids["agent_id"] = meta["agent_id"]

    msg_ids = []
    tool_call_ids = []
    task_ids = set()
    chain_ids = set()
    session_refs = set()

    for entry in history:
        msg_ids.append(entry["msg_id"])
        for tc in entry.get("tool_calls", []):
            tool_call_ids.append({"call_id": tc["call_id"], "tool": tc["name"]})
            tc_ids = tc.get("extracted_ids", {})
            if "task_id" in tc_ids:
                task_ids.add(tc_ids["task_id"])
            if "chain_id" in tc_ids:
                chain_ids.add(tc_ids["chain_id"])
        ext = entry.get("extracted_ids", {})
        for tid in ext.get("task_ids", []):
            task_ids.add(tid)
        for sid in ext.get("session_refs", []):
            session_refs.add(sid)

    ids["message_ids"] = msg_ids
    ids["tool_call_ids"] = tool_call_ids
    if task_ids:
        ids["task_ids"] = sorted(task_ids)
    if chain_ids:
        ids["chain_ids"] = sorted(chain_ids)
    if session_refs:
        ids["session_refs"] = sorted(session_refs)

    return ids


def print_ids(all_ids):
    print(c("bold", "═" * 80))
    print(c("bold", "  Extracted IDs"))
    print(c("bold", "═" * 80))

    print(f"  session_id:  {c('cyan', all_ids['session_id'])}")
    if "parent_id" in all_ids:
        print(f"  parent_id:   {c('cyan', all_ids['parent_id'])}")
    if "agent_id" in all_ids:
        print(f"  agent_id:    {c('magenta', all_ids['agent_id'])}")

    print()
    print(f"  {c('bold', 'Message IDs')} ({len(all_ids['message_ids'])})")
    for mid in all_ids["message_ids"]:
        print(f"    {mid}")

    if all_ids.get("tool_call_ids"):
        print()
        print(f"  {c('bold', 'Tool Call IDs')} ({len(all_ids['tool_call_ids'])})")
        for tc in all_ids["tool_call_ids"]:
            print(f"    {tc['call_id']}  ({c('yellow', tc['tool'])})")

    if all_ids.get("task_ids"):
        print()
        print(f"  {c('bold', 'Task IDs')} ({len(all_ids['task_ids'])})")
        for tid in all_ids["task_ids"]:
            print(f"    {c('green', tid)}")

    if all_ids.get("chain_ids"):
        print()
        print(f"  {c('bold', 'Chain IDs')} ({len(all_ids['chain_ids'])})")
        for cid in all_ids["chain_ids"]:
            print(f"    {c('blue', cid)}")

    if all_ids.get("session_refs"):
        print()
        print(f"  {c('bold', 'Session References')} ({len(all_ids['session_refs'])})")
        for sid in all_ids["session_refs"]:
            print(f"    {c('cyan', sid)}")

    print()


def build_json_output(session_id, meta, history, all_ids):
    output = {
        "session_id": session_id,
        "meta": meta,
        "ids": all_ids,
        "messages": [],
    }

    for entry in history:
        msg = {
            "msg_id": entry["msg_id"],
            "role": entry["role"],
            "content": entry["content"],
            "embedded": entry["embedded"],
        }
        if entry.get("model_id"):
            msg["model_id"] = entry["model_id"]
        if entry["tool_calls"]:
            msg["tool_calls"] = [
                {
                    "call_id": tc["call_id"],
                    "name": tc["name"],
                    "args": tc["args"],
                    "extracted_ids": tc["extracted_ids"],
                }
                for tc in entry["tool_calls"]
            ]
        if entry["tool_result_for"]:
            msg["tool_result_for"] = entry["tool_result_for"]
        if entry["errors"]:
            msg["errors"] = entry["errors"]
        if entry["extracted_ids"]:
            msg["extracted_ids"] = entry["extracted_ids"]
        output["messages"].append(msg)

    return output


def main():
    parser = argparse.ArgumentParser(
        description="Display conversation history of a FlowState session"
    )
    parser.add_argument(
        "session_id",
        nargs="?",
        help="Session ID (UUID or session-* format)",
    )
    parser.add_argument(
        "--detail",
        "-d",
        action="store_true",
        help="Detailed view with full content and arguments",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="Output full history as JSON",
    )
    parser.add_argument(
        "--ids-only",
        action="store_true",
        help="Extract and display only IDs found in the session",
    )
    parser.add_argument(
        "--latest",
        action="store_true",
        help="Show the most recent session",
    )

    args = parser.parse_args()

    session_id = args.session_id
    if args.latest:
        latest = find_latest_session()
        if not latest:
            print("No sessions found.", file=sys.stderr)
            sys.exit(1)
        session_id = latest["id"]

    if not session_id:
        parser.print_help()
        sys.exit(1)

    meta = load_session_meta(session_id)
    session_data = load_session_data(session_id)

    if not session_data:
        print(f"Session data not found for: {session_id}", file=sys.stderr)
        sys.exit(1)

    history = build_history(session_data)
    all_ids = collect_all_ids(session_id, meta, history)
    start_ts, end_ts = load_recording_timestamps(session_id)

    if args.json:
        output = build_json_output(session_id, meta, history, all_ids)
        print(json.dumps(output, indent=2, default=str))
        return

    if args.ids_only:
        print_ids(all_ids)
        return

    print_header(session_id, meta, history, start_ts, end_ts)

    if args.detail:
        print_detail(history)
    else:
        print_short(history)

    print(c("bold", "─" * 80))


if __name__ == "__main__":
    main()
