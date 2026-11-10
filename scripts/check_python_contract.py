#!/usr/bin/env python3
"""Check changed committed Python source structure and test-file ownership."""
import argparse
import ast
import sys

from scripts.contract_sources import git, read_blob, snapshot
from scripts.python_contract_ast import discoverable_test, source_violations


def owning_test(path):
    """Map each implementation module to its single owning test file."""
    parent, stem = path.rsplit("/", 1)
    return f"{parent}/tests/test_{stem}"


def owning_implementation(test_path):
    """Resolve the implementation of a root or nested test module."""
    parent, separator, name = test_path.rpartition('/tests/test_')
    return f"{parent}/{name}" if separator and name else None


def changed_python_paths(base):
    """Include additions, changes, type changes and deletions; split renames."""
    changed = git('diff', '--name-only', '-z', '--no-renames', '--diff-filter=ACMRTD',
                  base, 'HEAD', '--', '*.py').split(bytes([0]))
    return list(map(bytes.decode, filter(None, changed)))


def test_violations(path, tree):
    """Require a regular Python test file with a discoverable test entry point."""
    try:
        module = ast.parse(read_blob(tree, path), filename=path)
    except (KeyError, ValueError, UnicodeError, SyntaxError) as error:
        return [f"{path}: invalid owning test: {error}"]
    return [] if discoverable_test(module) else [
        f"{path}: no discoverable test_* function or method present"
    ]


def candidate_errors(base):
    """Use the committed candidate tree and an exact SHA base, never local edits."""
    tree, _ = snapshot(base, '*.py')
    scripts = sorted(filter(lambda path: path.startswith('scripts/'),
                            changed_python_paths(base)))
    failures = []
    for path in scripts:
        if '/tests/' in path:
            owner = owning_implementation(path)
            if not owner:
                if path.rsplit('/', 1)[-1].startswith('test_'):
                    failures.append(f"{path}: unsupported owning test layout")
                continue
            if path not in tree:
                if owner in tree:
                    failures.append(f"{owner}: missing owning test {path}")
                continue
            if owner not in tree:
                failures.append(f"{path}: no matching implementation {owner}")
            failures.extend(test_violations(path, tree))
            continue
        if path.endswith('/__init__.py'):
            continue
        required = owning_test(path)
        if path not in tree:
            if required in tree:
                failures.append(f"{required}: no matching implementation {path}")
            continue
        if required not in tree:
            failures.append(f"{path}: missing owning test {required}")
        else:
            failures.extend(test_violations(required, tree))
        try:
            failures.extend(source_violations(path, read_blob(tree, path)))
        except (KeyError, ValueError, UnicodeError) as error:
            failures.append(f"{path}: invalid committed source: {error}")
    return failures


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True, help='exact protected PR base SHA')
    args = parser.parse_args()
    try:
        failures = candidate_errors(args.base)
    except (ValueError, OSError) as error:
        failures = [str(error)]
    for failure in failures:
        print('FAIL: ' + failure, file=sys.stderr)
    return int(bool(failures))


if __name__ == '__main__':
    raise SystemExit(main())
