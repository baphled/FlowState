"""Bound the complete committed review diff; no path or label exemptions."""
import argparse
import difflib
import json
import re
import subprocess
import sys

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


def blob(sha, mode):
    if set(sha) == {'0'}:
        return b''
    if mode not in ('100644', '100755', '120000'):
        raise ValueError('Unsupported changed object mode: ' + mode)
    if int(git('cat-file', '-s', sha)) > MAX_BLOB_BYTES:
        raise ValueError('Changed blob exceeds 1 MiB; split or review separately')
    return git('cat-file', 'blob', sha)


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
        if binary(old) or binary(new):
            changed_bytes += len(old) + len(new)
            continue
        old_lines, new_lines = old.splitlines(keepends=True), new.splitlines(keepends=True)
        changes = difflib.SequenceMatcher(None, old_lines, new_lines, autojunk=False)
        for tag, first, last, start, end in changes.get_opcodes():
            if tag != 'equal':
                lines += last - first + end - start
                changed_bytes += sum(map(len, old_lines[first:last] + new_lines[start:end]))
    return {'files': files, 'changed_lines': lines, 'changed_bytes': changed_bytes}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True)
    parser.add_argument('--head', required=True)
    args = parser.parse_args()
    try:
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
