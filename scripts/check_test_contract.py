#!/usr/bin/env python3
"""Check changed test sources; unchanged legacy suites remain covered during migration."""
import argparse
import json
import re
import subprocess
import sys
from pathlib import Path


def git(*args):
    return subprocess.check_output(['git', *args])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True)
    parser.add_argument('--worktree', action='store_true', help='explicit precommit preview, not candidate evidence')
    args = parser.parse_args()
    if not re.fullmatch('[0-9a-f]{40}', args.base):
        parser.error('base must be the exact 40-character PR base SHA')
    if git('rev-parse', '--show-prefix').strip():
        parser.error('run from the repository root')
    subprocess.run(['git', 'merge-base', '--is-ancestor', args.base, 'HEAD'], check=True)
    if args.worktree:
        names = git('diff', '--name-only', '-z', '--no-renames', args.base, '--', '*_test.go').split(b'\0')
        names += git('ls-files', '--others', '--exclude-standard', '-z', '--', '*_test.go').split(b'\0')
        files = []
        for name in sorted(set(filter(None, names))):
            path = name.decode('utf-8')
            if Path(path).is_symlink():
                raise ValueError('test sources must be regular files: ' + path)
            if Path(path).is_file():
                files.append({'Name': path, 'Source': Path(path).read_text()})
    else:
        names = git('diff', '--name-only', '-z', '--no-renames', '--diff-filter=ACMT', args.base, 'HEAD', '--', '*_test.go').split(b'\0')
        modes = {}
        for row in filter(None, git('ls-tree', '-rz', 'HEAD').split(b'\0')):
            attrs, name = row.split(b'\t', 1)
            modes[name] = attrs.split()[0]
        files = []
        for name in filter(None, names):
            path = name.decode('utf-8')
            if modes.get(name) not in (b'100644', b'100755'):
                raise ValueError('test sources must be regular committed files: ' + path)
            files.append({'Name': path, 'Source': git('show', 'HEAD:' + path).decode('utf-8')})
    return subprocess.run(['go', 'run', './scripts/test-contract'], input=json.dumps(files).encode()).returncode


if __name__ == '__main__':
    sys.exit(main())
