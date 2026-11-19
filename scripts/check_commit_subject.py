#!/usr/bin/env python3
"""Validate one commit subject against the committed commitlint policy.

CI runs commitlint over every PR commit (Lint Commits) and over the PR
title (PR Policy); this checker enforces the same error-level rules at
commit time so invalid titles fail locally instead of in CI. The rules
are read from .commitlintrc.json — the single policy source shared with
CI — so the two gates cannot drift.

Enforced error-level rules: type-enum, scope-enum, subject-case
(lower-case), subject-empty, subject-max-length, header-max-length.
Merge and Revert headers follow the conventional parser's special
handling and are accepted. Lower-severity rules (e.g. scope-empty at
level 1) remain warnings in CI only.
"""
import argparse
import json
import re
import sys

HEADER_PARSE = re.compile(
    r"^(?P<type>[a-zA-Z]+)(?:\((?P<scope>[^)]*)\))?(?P<bang>!)?: (?P<subject>.*)$")
MERGE_HEADER = re.compile(r"^Merge (branch|pull request|remote-tracking)?")
REVERT_HEADER = re.compile(r'^Revert ["“]')

DEFAULT_CONFIG = {
    "type-enum": [2, "always", ["feat", "fix", "docs", "refactor", "test",
                                "chore", "perf", "ci", "build", "revert", "style"]],
    "scope-enum": [2, "always", []],
    "subject-case": [2, "always", "lower-case"],
    "subject-empty": [2, "never"],
    "subject-max-length": [2, "always", 72],
    "header-max-length": [2, "always", 100],
}


def load_config(path):
    """Read the commitlint JSON config, falling back to defaults."""
    try:
        with open(path, encoding="utf-8") as handle:
            return {**DEFAULT_CONFIG, **json.load(handle).get("rules", {})}
    except (OSError, ValueError):
        return dict(DEFAULT_CONFIG)


def error_level(rule):
    """Return the severity of a commitlint rule tuple, or 0 when absent."""
    return rule[0] if isinstance(rule, list) and rule else 0


def type_violation(parts, config):
    """Return the type-enum violation, or None."""
    allowed = config.get("type-enum", [])
    if error_level(allowed) >= 2 and parts["type"] not in allowed[2]:
        return f"type must be one of {allowed[2]}"
    return None


def scope_violation(parts, config):
    """Return the scope-enum violation, or None."""
    allowed = config.get("scope-enum", [])
    if parts["scope"] is not None and error_level(allowed) >= 2 \
            and allowed[2] and parts["scope"] not in allowed[2]:
        return f"scope must be one of {allowed[2]}"
    return None


def empty_violation(subject, config):
    """Return the subject-empty violation, or None."""
    if error_level(config.get("subject-empty", [])) >= 2 and not subject.strip():
        return "subject may not be empty"
    return None


def subject_length_violation(subject, config):
    """Return the subject-max-length violation, or None."""
    max_subject = config.get("subject-max-length", [])
    if error_level(max_subject) >= 2 and len(subject) > max_subject[2]:
        return f"subject must be at most {max_subject[2]} characters"
    return None


def header_length_violation(header, config):
    """Return the header-max-length violation, or None."""
    max_header = config.get("header-max-length", [])
    if error_level(max_header) >= 2 and len(header) > max_header[2]:
        return f"header must be at most {max_header[2]} characters"
    return None


def length_violations(parts, header, config):
    """Return the empty/length violations, or an empty list."""
    subject = parts["subject"]
    checks = (empty_violation(subject, config),
              subject_length_violation(subject, config),
              header_length_violation(header, config))
    return [violation for violation in checks if violation]


def case_violation(parts, config):
    """Return the lower-case violation, or None."""
    case_rule = config.get("subject-case", [])
    if error_level(case_rule) >= 2 and case_rule[2] == "lower-case" \
            and parts["subject"] != parts["subject"].lower():
        return "subject must be lower-case"
    return None


def validate_header(header, config):
    """Return the list of violations for one commit subject header."""
    if MERGE_HEADER.match(header) or REVERT_HEADER.match(header):
        return []
    match = HEADER_PARSE.match(header)
    if match is None:
        return ["subject may not be empty"]
    parts = match.groupdict()
    errors = []
    for violation in (type_violation(parts, config),
                      scope_violation(parts, config),
                      case_violation(parts, config),
                      *length_violations(parts, header, config)):
        if violation:
            errors.append(violation)
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--message-file", help="git commit-msg message file")
    parser.add_argument("--header", action="append", default=[],
                        help="one commit subject to validate")
    parser.add_argument("--config", default=".commitlintrc.json")
    arguments = parser.parse_args()

    headers = list(arguments.header)
    if arguments.message_file:
        with open(arguments.message_file, encoding="utf-8") as handle:
            headers.insert(0, handle.read().split("\n", 1)[0])

    config = load_config(arguments.config)
    failed = False
    for header in headers:
        for violation in validate_header(header, config):
            failed = True
            print(f"{violation}: {header}", file=sys.stderr)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
