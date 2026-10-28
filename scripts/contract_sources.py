"""Read exact committed candidates for repository contract checks."""
import re
import subprocess


def git(*args):
    return subprocess.check_output(['git', *args])


def snapshot(base, pattern):
    if not re.fullmatch('[0-9a-f]{40}', base):
        raise ValueError('base must be the exact 40-character PR base SHA')
    if git('rev-parse', '--show-prefix').strip():
        raise ValueError('run from the repository root')
    subprocess.run(['git', 'merge-base', '--is-ancestor', base, 'HEAD'], check=True)
    tree = {}
    for row in filter(None, git('ls-tree', '-rz', 'HEAD').split(b'\0')):
        attrs, name = row.split(b'\t', 1)
        mode, kind, oid = attrs.split()
        tree[name.decode()] = (mode, kind, oid)
    changed = git('diff', '--name-only', '-z', '--no-renames', '--diff-filter=ACMT', base, 'HEAD', '--', pattern).split(b'\0')
    return tree, [n.decode() for n in filter(None, changed)]


def read_blob(tree, name):
    mode, kind, oid = tree[name]
    if mode not in (b'100644', b'100755') or kind != b'blob':
        raise ValueError('contract inputs must be regular committed files: ' + name)
    return git('cat-file', 'blob', oid.decode()).decode('utf-8')
