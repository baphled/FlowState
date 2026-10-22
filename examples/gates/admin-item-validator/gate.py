#!/usr/bin/env python3
"""
admin-item-validator — ext-gate v0

Validates that every task in the life-admin-lead's triage output has:
  - a non-empty "deadline" field (ISO date string or descriptive text)
  - an integer "priority" field in the range 1–4

Input  (stdin):  {"kind":"admin-item-validator","payload":<output>,"policy":{}}
Output (stdout): {"pass": true}
              or {"pass": false, "reason": "...", "incomplete": ["<title>", ...]}
"""
import json
import sys


def main() -> None:
    try:
        req = json.load(sys.stdin)
    except json.JSONDecodeError as exc:
        json.dump({"pass": False, "reason": f"invalid JSON input: {exc}"}, sys.stdout)
        return

    payload = req.get("payload") or {}

    # Support payload as a raw string (agent may serialise the whole output as text)
    if isinstance(payload, str):
        try:
            payload = json.loads(payload)
        except json.JSONDecodeError:
            json.dump(
                {"pass": False, "reason": "payload is a non-JSON string; expected object with 'tasks' key"},
                sys.stdout,
            )
            return

    tasks = payload.get("tasks")
    if not isinstance(tasks, list):
        json.dump(
            {"pass": False, "reason": "payload missing 'tasks' list"},
            sys.stdout,
        )
        return

    if len(tasks) == 0:
        json.dump({"pass": False, "reason": "tasks list is empty"}, sys.stdout)
        return

    incomplete = []
    for task in tasks:
        if not isinstance(task, dict):
            incomplete.append("<non-object task entry>")
            continue

        title = task.get("title") or "<untitled>"
        deadline = task.get("deadline")
        priority = task.get("priority")

        missing = []
        if not deadline or (isinstance(deadline, str) and not deadline.strip()):
            missing.append("deadline")
        if priority is None:
            missing.append("priority")
        elif not isinstance(priority, int) or not (1 <= priority <= 4):
            missing.append("priority (must be integer 1–4)")

        if missing:
            incomplete.append(f"{title} (missing: {', '.join(missing)})")

    if incomplete:
        reason = f"{len(incomplete)} task(s) failed validation: {'; '.join(incomplete)}"
        json.dump({"pass": False, "reason": reason, "incomplete": incomplete}, sys.stdout)
    else:
        json.dump({"pass": True}, sys.stdout)


if __name__ == "__main__":
    main()
