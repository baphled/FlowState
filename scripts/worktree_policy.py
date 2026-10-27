#!/usr/bin/env python3
"""Create registered feature worktrees and remove clean pushed ones after approval."""

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

from project_metadata import REFERENCE


def git(*args, cwd=None):
    return subprocess.check_output(['git', *args], cwd=cwd, text=True).strip()


def bare_root():
    value = Path(git('rev-parse', '--path-format=absolute', '--git-common-dir')).resolve()
    if git('--git-dir', str(value), 'rev-parse', '--is-bare-repository') != 'true':
        raise ValueError('Use the registered bare repository (normally ~/Projects/FlowState.git), not a separate clone')
    return value


def path_for(root, name):
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_-]*', name):
        raise ValueError('NAME must be a simple feature directory name, without slashes or dot segments')
    target = root / name
    if target.is_symlink() or target.resolve().parent != root:
        raise ValueError('worktree must be an immediate directory inside the registered bare root')
    return target


def create(root, name):
    target = path_for(root, name)
    git('--git-dir', str(root), 'fetch', 'origin', 'refs/heads/next:refs/remotes/origin/next')
    base = git('--git-dir', str(root), 'rev-parse', 'refs/remotes/origin/next')
    git('--git-dir', str(root), 'worktree', 'add', '-b', 'feature/' + name, str(target), base)
    print(json.dumps({'worktree': str(target), 'base_sha': base, 'branch': 'feature/' + name}))


def cleanup(root, name, record_path):
    target = path_for(root, name)
    if record_path.resolve().is_relative_to(root):
        raise ValueError('approval metadata/read-back copy must stay outside the code repository')
    record = json.loads(record_path.read_text())
    if not REFERENCE.fullmatch(record.get('metadata_ref', '')) or not re.fullmatch(
            r'https://github.com/baphled/FlowState/pull/[1-9][0-9]*', record.get('pr_url', '')):
        raise ValueError('external Dropbox record and actual FlowState PR reference required')
    if record.get('state') not in ('approved', 'merged') or not record.get('github_evidence_ref'):
        raise ValueError('coordinator must re-query actual GitHub approval for the exact head first')
    registered = git('--git-dir', str(root), 'worktree', 'list', '--porcelain')
    if 'worktree ' + str(target) + '\n' not in registered + '\n':
        raise ValueError('target is not a registered worktree of this bare repository')
    branch = git('branch', '--show-current', cwd=target)
    head = git('rev-parse', 'HEAD', cwd=target)
    if not branch or branch in ('main', 'next') or record.get('approved_head_sha') != head or record.get('branch') != branch:
        raise ValueError('protected/detached branch or approval does not match the current exact head')
    flags = git('ls-files', '-v', '-z', cwd=target).split('\0')
    if any(row and (row[0].islower() or row.startswith('S ')) for row in flags):
        raise ValueError('index flags may hide local changes; preserve and reconcile them before cleanup')
    if git('status', '--porcelain', '--untracked-files=all', '--ignored=matching', cwd=target):
        raise ValueError('worktree contains modified, untracked or ignored files; preserve them before cleanup')
    remote = git('--git-dir', str(root), 'ls-remote', '--heads', 'origin', 'refs/heads/' + branch)
    if not remote:
        pr_number = record['pr_url'].rsplit('/', 1)[1]
        remote = git('--git-dir', str(root), 'ls-remote', 'origin', 'refs/pull/' + pr_number + '/head')
    if not remote or remote.split()[0] != head:
        raise ValueError('current feature head is not safely pushed/reachable through its PR; no cleanup performed')
    git('--git-dir', str(root), 'worktree', 'remove', str(target))
    deleted_branch = False
    if record['state'] == 'merged':
        git('--git-dir', str(root), 'fetch', 'origin', 'refs/heads/next:refs/remotes/origin/next', cwd=root)
        result = subprocess.run(['git', '--git-dir', str(root), 'merge-base', '--is-ancestor',
                                 head, 'refs/remotes/origin/next'], cwd=root, check=False)
        if result.returncode == 0:
            result = subprocess.run(['git', '--git-dir', str(root), 'branch', '-d', branch],
                                    cwd=root, capture_output=True, text=True, check=False)
            deleted_branch = result.returncode == 0
    print(json.dumps({'removed_worktree': str(target), 'branch_deleted': deleted_branch,
                      'branch_retained_until_merged': not deleted_branch,
                      'limit': 'local record shape is not authenticated GitHub approval; coordinator must query GitHub and read Dropbox'}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['new', 'cleanup'])
    parser.add_argument('name')
    parser.add_argument('--approval-record', type=Path)
    args = parser.parse_args()
    try:
        root = bare_root()
        if args.action == 'new':
            create(root, args.name)
        else:
            if not args.approval_record:
                raise ValueError('cleanup needs an external Dropbox approval record read back after querying GitHub')
            cleanup(root, args.name, args.approval_record)
        return 0
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print('FAIL: ' + str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
