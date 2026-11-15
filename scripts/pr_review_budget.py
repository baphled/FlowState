#!/usr/bin/env python3
"""Select a protected target's PR line budget by validated change type.

CI obtains --policy from git show <protected-base>:.env.ci. A PR's title
must agree with its source branch; neither PR labels nor candidate .env
files can change the selected budget. Push events can omit --branch.
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


def select(policy, title, branch=None):
    """Return a valid budget or fail closed on misclassification/configuration."""
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
    match = TITLE.match(title)
    if not match or match.group(1) not in TYPES:
        raise ValueError('Conventional change type is required')
    kind = match.group(1)
    if branch is not None and branch.split('/', 1)[0] != TYPES[kind]:
        raise ValueError('PR title and branch change types disagree')
    default = values.get('FLOWSTATE_PR_MAX_LINES')
    budget = values.get('FLOWSTATE_PR_MAX_LINES_' + kind.upper(), default)
    if budget is None or not POSITIVE.fullmatch(budget):
        raise ValueError('Invalid protected review budget for ' + kind)
    return int(budget)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--policy', type=Path, required=True)
    parser.add_argument('--title', required=True)
    parser.add_argument('--branch')
    args = parser.parse_args()
    try:
        print(select(args.policy.read_text(encoding='utf-8'), args.title, args.branch))
    except (OSError, ValueError) as exc:
        print('PR review budget blocked: ' + str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
