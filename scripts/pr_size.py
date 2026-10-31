"""Bound the complete committed review diff; no path or label exemptions.

Additive --live mode measures the current working tree (staged, unstaged and
untracked) against the PR budget without requiring a commit, reusing the same
counting semantics and constants as the committed-diff mode.
"""
import argparse
import difflib
import json
import os
import re
import subprocess
import sys
import tempfile

MAX_LINES = 500
MAX_FILES = 10
MAX_CHANGED_BYTES = 128 * 1024
MAX_BLOB_BYTES = 1024 * 1024


def git(*args):
    return subprocess.check_output(['git', *args], stderr=subprocess.PIPE)


def commit(value):
    if not re.fullmatch(r'[0-9a-f]{40}', value):
        raise ValueError('Use the actual full base/head commit SHA')
    git('rev-parse', '--verify', value + '^{commit}')
    return value


def blob_at(git_fn, sha, mode):
    if set(sha) == {'0'}:
        return b''
    if mode not in ('100644', '100755', '120000'):
        raise ValueError('Unsupported changed object mode: ' + mode)
    if int(git_fn('cat-file', '-s', sha)) > MAX_BLOB_BYTES:
        raise ValueError('Changed blob exceeds 1 MiB; split or review separately')
    return git_fn('cat-file', 'blob', sha)


def blob(sha, mode):
    return blob_at(git, sha, mode)


def diff_lines(old, new):
    """Shared blob-diff counting: returns (changed_lines, changed_bytes)."""
    if binary(old) or binary(new):
        return 0, len(old) + len(new)
    lines = 0
    changed_bytes = 0
    old_lines, new_lines = old.splitlines(keepends=True), new.splitlines(keepends=True)
    changes = difflib.SequenceMatcher(None, old_lines, new_lines, autojunk=False)
    for tag, first, last, start, end in changes.get_opcodes():
        if tag != 'equal':
            lines += last - first + end - start
            changed_bytes += sum(map(len, old_lines[first:last] + new_lines[start:end]))
    return lines, changed_bytes


def binary(value):
    try:
        value.decode('utf-8')
    except UnicodeDecodeError:
        return True
    return b'\0' in value


def measure(base, head):
    base, head = commit(base), commit(head)
    if subprocess.run(['git', 'merge-base', '--is-ancestor', base, head],
                      capture_output=True).returncode:
        raise ValueError('Base is not an ancestor; refresh onto the current target')
    raw = git('diff', '--raw', '--no-abbrev', '--no-renames', '--ignore-submodules=none',
              '-z', base, head, '--')
    records = raw.split(b'\0')[:-1]
    files, lines, changed_bytes = 0, 0, 0
    for index in range(0, len(records), 2):
        old_mode, new_mode, old_sha, new_sha, _ = records[index].decode().split()
        old = blob(old_sha, old_mode.lstrip(':'))
        new = blob(new_sha, new_mode)
        files += 1
        part_lines, part_bytes = diff_lines(old, new)
        lines += part_lines
        changed_bytes += part_bytes
    return {'files': files, 'changed_lines': lines, 'changed_bytes': changed_bytes}


def resolve_base(value):
    """Resolve a ref or SHA to a full commit SHA (live mode accepts refs)."""
    git('rev-parse', '--verify', value + '^{commit}')
    return git('rev-parse', value).strip().decode()


def live_tree():
    """Write a tree object capturing the full working state (staged, unstaged,
    untracked) into the repo ODB and return its SHA."""
    index = git('write-tree').strip().decode()
    git_dir = git('rev-parse', '--absolute-git-dir').strip().decode()
    with tempfile.TemporaryDirectory() as tmp:
        env = dict(os.environ, GIT_INDEX_FILE=os.path.join(tmp, 'live-index'),
                   GIT_DIR=git_dir, GIT_WORK_TREE=os.path.abspath('.'))
        subprocess.check_output(['git', 'read-tree', index], env=env,
                                stderr=subprocess.PIPE)
        subprocess.check_output(['git', 'add', '-A', '--'], env=env,
                                stderr=subprocess.PIPE)
        return subprocess.check_output(['git', 'write-tree'], env=env,
                                       stderr=subprocess.PIPE).strip().decode()


def measure_live(base):
    """Measure committed base -> current working tree with shared semantics."""
    base = resolve_base(base)
    tree = live_tree()
    raw = git('diff-tree', '-r', '--no-abbrev', '--no-renames', '--ignore-submodules=none',
              '-z', base, tree, '--')
    records = raw.split(b'\0')[:-1]
    files, lines, changed_bytes = 0, 0, 0
    for index in range(0, len(records), 2):
        old_mode, new_mode, old_sha, new_sha, _ = records[index].decode().split()
        old = blob_at(git, old_sha, old_mode.lstrip(':'))
        new = blob_at(git, new_sha, new_mode)
        files += 1
        part_lines, part_bytes = diff_lines(old, new)
        lines += part_lines
        changed_bytes += part_bytes
    return {'files': files, 'changed_lines': lines, 'changed_bytes': changed_bytes}


def format_live(totals):
    """Progressive one-line summary; returns (text, exceeded_keys)."""
    limits = {'files': MAX_FILES, 'changed_lines': MAX_LINES,
              'changed_bytes': MAX_CHANGED_BYTES}
    exceeded = [key for key in limits if totals[key] > limits[key]]
    text = '{}/{} lines, {}/{} files'.format(totals['changed_lines'], MAX_LINES,
                                             totals['files'], MAX_FILES)
    if exceeded:
        worst = max(exceeded, key=lambda key: totals[key] - limits[key])
        unit = {'changed_lines': 'lines', 'files': 'files',
                'changed_bytes': 'bytes'}[worst]
        text += ' — OVER by {} {} — split before committing further'.format(
            totals[worst] - limits[worst], unit)
    else:
        text += ' — OK'
    return text, exceeded


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True)
    parser.add_argument('--head')
    parser.add_argument('--live', action='store_true',
                        help='measure the working tree (staged+unstaged+untracked) vs'
                             ' --base; no commit required')
    args = parser.parse_args()
    if not args.live and not args.head:
        parser.error('--head is required unless --live is used')
    try:
        if args.live:
            totals = measure_live(args.base)
            limits = {'files': MAX_FILES, 'changed_lines': MAX_LINES,
                      'changed_bytes': MAX_CHANGED_BYTES}
            exceeded = [key for key in limits if totals[key] > limits[key]]
            print(json.dumps({'totals': totals, 'limits': limits, 'exceeded': exceeded}))
            print(format_live(totals)[0])
            if exceeded:
                print('Split into one bounded behaviour with its tests per PR.',
                      file=sys.stderr)
            return bool(exceeded)
        totals = measure(args.base, args.head)
        limits = {'files': MAX_FILES, 'changed_lines': MAX_LINES,
                  'changed_bytes': MAX_CHANGED_BYTES}
        exceeded = [key for key in limits if totals[key] > limits[key]]
        print(json.dumps({'totals': totals, 'limits': limits, 'exceeded': exceeded}))
        if exceeded:
            print('Split into one bounded behaviour with its tests per PR.', file=sys.stderr)
        return bool(exceeded)
    except (ValueError, subprocess.CalledProcessError) as error:
        print('PR size check failed: ' + str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
