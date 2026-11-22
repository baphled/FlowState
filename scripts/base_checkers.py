#!/usr/bin/env python3
"""Run the contract checkers exactly as they are on the PR base, against the checked-out tree.

A pull request must not be able to rewrite the checker that judges it, so the
checker code and its configuration are read from the protected base commit,
never from the checkout. The base copy is materialised in a private directory
and executed there with the repository's own git database, so the checkers
still read the candidate (HEAD) objects but run base code. Every failure to
produce the base copy is fatal: there is no fallback to the checkout.
"""
import argparse
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path, PurePosixPath

CHECKER_FILES = (
    'go.mod', 'go.sum',
    'scripts/check_code_contract.py', 'scripts/check_test_contract.py', 'scripts/check_python_contract.py',
    'scripts/contract_sources.py', 'scripts/python_contract_ast.py', 'scripts/test_layout.py',
)
CHECKER_TREES = ('scripts/code-contract', 'scripts/test-contract')
CONFIG_FILE = '.golangci-contract.yml'
REGULAR_MODES = (b'100644', b'100755')


class BaseCopyError(Exception):
    """The base copy of the checkers could not be produced; callers must fail closed."""


def git(*args, env=None):
    """Run git in the current repository and return raw stdout.

    Replacement refs are switched off, so a local `refs/replace` entry cannot change what is read
    from the base."""
    return subprocess.check_output(['git', *args], env=dict(env or os.environ, GIT_NO_REPLACE_OBJECTS='1'))


def check_base(base):
    """Require an exact commit SHA that is an ancestor of HEAD."""
    if not re.fullmatch('[0-9a-f]{40}', base):
        raise BaseCopyError('base must be the exact 40-character PR base SHA')
    if subprocess.run(['git', 'merge-base', '--is-ancestor', base, 'HEAD']).returncode:
        raise BaseCopyError('base is not an ancestor of HEAD')


def base_entries(base, paths):
    """Map each regular blob under the given base paths to its object id."""
    out = git('ls-tree', '-rz', base, '--', *paths)
    entries = {}
    for row in filter(None, out.split(b'\0')):
        attrs, name = row.split(b'\t', 1)
        mode, kind, oid = attrs.split()
        if mode not in REGULAR_MODES or kind != b'blob':
            raise BaseCopyError('checker inputs must be regular files: ' + name.decode())
        entries[name.decode()] = oid.decode()
    return entries


def materialise(base, dest):
    """Write the base copy of every checker path into dest; fail if any is absent."""
    check_base(base)
    entries = base_entries(base, CHECKER_FILES + CHECKER_TREES)
    missing = [p for p in CHECKER_FILES if p not in entries]
    missing += [t for t in CHECKER_TREES if not any(PurePosixPath(t) in PurePosixPath(e).parents for e in entries)]
    if missing:
        raise BaseCopyError('base ' + base + ' lacks checker paths: ' + ', '.join(missing))
    for name, oid in entries.items():
        target = Path(dest, name)
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(git('cat-file', 'blob', oid))


def run_in_base_copy(base, command):
    """Run command inside the base copy with the real repository as its git database."""
    git_dir = git('rev-parse', '--absolute-git-dir').decode().strip()
    with tempfile.TemporaryDirectory(prefix='base-checkers-') as dest:
        materialise(base, dest)
        env = {k: v for k, v in os.environ.items() if k not in ('PYTHONPATH', 'PYTHONHOME')}
        env.update(GIT_DIR=git_dir, GIT_WORK_TREE=dest, GOWORK='off', GIT_NO_REPLACE_OBJECTS='1')
        env['GOFLAGS'] = (env.get('GOFLAGS', '') + ' -buildvcs=false -mod=readonly').strip()
        try:
            return subprocess.run(command, cwd=dest, env=env).returncode
        except OSError as error:
            raise BaseCopyError('cannot run ' + command[0] + ': ' + str(error)) from error


def export_config(base, dest):
    """Write the base copy of the contract linter configuration to dest (a .yml or .yaml path).

    The linter takes the configuration type from the file extension and rejects any other name, so a
    destination without one is refused up front rather than failing the lint step later.
    """
    if Path(dest).suffix not in ('.yml', '.yaml'):
        raise BaseCopyError('linter configuration destination needs a .yml or .yaml name: ' + str(dest))
    check_base(base)
    entries = base_entries(base, (CONFIG_FILE,))
    if CONFIG_FILE not in entries:
        raise BaseCopyError('base ' + base + ' lacks ' + CONFIG_FILE)
    try:
        Path(dest).write_bytes(git('cat-file', 'blob', entries[CONFIG_FILE]))
    except OSError as error:
        raise BaseCopyError('cannot write the linter configuration: ' + str(error)) from error


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='action', required=True)
    run = sub.add_parser('run', help='run a command, given after --, from the base copy')
    run.add_argument('--base', required=True)
    run.add_argument('command', nargs=argparse.REMAINDER)
    config = sub.add_parser('config', help='write the base contract linter configuration')
    config.add_argument('--base', required=True)
    config.add_argument('--dest', required=True)
    args = parser.parse_args(argv)
    try:
        if args.action == 'config':
            export_config(args.base, args.dest)
            return 0
        command = args.command[1:] if args.command[:1] == ['--'] else args.command
        if not command:
            parser.error('run needs a command after --')
        return run_in_base_copy(args.base, command)
    except (BaseCopyError, subprocess.CalledProcessError) as error:
        print('FAIL: cannot use the base checkers: ' + str(error), file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
