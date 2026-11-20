#!/usr/bin/env python3
"""Select a protected target's PR review budgets by validated change type.

Two budgets are selected from one trusted policy with identical
validation semantics: the changed-line budget (FLOWSTATE_PR_MAX_LINES,
with per-kind overrides such as FLOWSTATE_PR_MAX_LINES_FEAT) and the
changed-path budget (FLOWSTATE_PR_MAX_FILES, with per-kind overrides
such as FLOWSTATE_PR_MAX_FILES_FEAT). Neither PR labels nor candidate
.env files can change a selected budget.

CI obtains --policy from git show <protected-base>:.env.ci. A PR's title
must agree with its source branch. Push events can omit --branch.

Exit codes: 0 selects a budget; 1 rejects policy/configuration/IO
failure; 2 reports an unclassifiable subject (missing or unrecognised
Conventional change type, or title/branch change-type disagreement) so
push jobs can skip the PR-scoped gate while CI fails everything else.
"""
import argparse
import re
import sys
from pathlib import Path

TYPES = {'feat': 'feature', 'fix': 'fix', 'chore': 'chore',
         'docs': 'docs', 'refactor': 'refactor', 'test': 'test',
         'perf': 'perf', 'revert': 'revert'}
TITLE = re.compile(r'^([a-z]+)(?:\([a-z0-9-]+\))?!?: .+')
POSITIVE = re.compile(r'[1-9][0-9]{0,5}\Z')


class ClassificationError(ValueError):
    """Subject-classification failure; push CI may skip only this class."""


def policy_values(policy):
    """Parse the trusted budget policy, rejecting malformed or duplicate keys."""
    values = {}
    for line in policy.splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        if '=' not in line:
            raise ValueError('Malformed protected budget configuration')
        key, value = line.split('=', 1)
        if key in values:
            raise ValueError('Duplicate protected budget key: ' + key)
        values[key] = value
    return values


def change_kind(title, branch=None):
    """Classify the subject and enforce title/branch change-type agreement."""
    match = TITLE.match(title)
    if not match or match.group(1) not in TYPES:
        raise ClassificationError('Conventional change type is required')
    kind = match.group(1)
    if branch is not None and branch.split('/', 1)[0] != TYPES[kind]:
        raise ClassificationError('PR title and branch change types disagree')
    return kind


def protected_budget(values, base_key, kind):
    """Resolve one budget family: per-kind override, else the kind default."""
    budget = values.get(base_key + '_' + kind.upper(), values.get(base_key))
    if budget is None or not POSITIVE.fullmatch(budget):
        raise ValueError('Invalid protected review budget for ' + kind)
    return int(budget)


def select(policy, title, branch=None):
    """Return a valid line budget or fail closed on misclassification/configuration."""
    return protected_budget(policy_values(policy), 'FLOWSTATE_PR_MAX_LINES',
                            change_kind(title, branch))


def select_files(policy, title, branch=None):
    """Return a valid changed-path budget with select()'s exact semantics."""
    return protected_budget(policy_values(policy), 'FLOWSTATE_PR_MAX_FILES',
                            change_kind(title, branch))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--policy', type=Path, required=True)
    parser.add_argument('--title', required=True)
    parser.add_argument('--branch')
    parser.add_argument('--files', action='store_true',
                        help='select the changed-path budget instead of changed lines')
    args = parser.parse_args()
    try:
        chooser = select_files if args.files else select
        print(chooser(args.policy.read_text(encoding='utf-8'), args.title, args.branch))
    except ClassificationError as exc:
        print('PR review budget blocked: ' + str(exc), file=sys.stderr)
        return 2
    except (OSError, ValueError) as exc:
        print('PR review budget blocked: ' + str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
