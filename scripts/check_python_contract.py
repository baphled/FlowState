#!/usr/bin/env python3
"""Check changed committed Python source structure and test-file ownership."""
import argparse
import ast
import os
import re
import subprocess
import sys
import tempfile

from scripts.contract_sources import git, read_blob, snapshot
from scripts.python_contract_ast import discoverable_test, source_violations

RECURSION_ENV = 'FLOWSTATE_PY_CONTRACT_EXEC'

SELF_MODULE = 'scripts/check_python_contract.py'

RUNNER = '''
import importlib, sys, unittest
module = importlib.import_module(sys.argv[1])
suite = unittest.TestSuite()
for name in dir(module):
    item = getattr(module, name)
    if isinstance(item, type) and issubclass(item, unittest.TestCase):
        suite.addTests(unittest.TestLoader().loadTestsFromTestCase(item))
    elif name.startswith('test_') and callable(item):
        if getattr(item, '__module__', None) == module.__name__:
            suite.addTest(unittest.FunctionTestCase(item))
result = unittest.TextTestRunner(verbosity=0).run(suite)
print('RESULT %d %d' % (result.testsRun, len(result.skipped)))
raise SystemExit(0 if result.wasSuccessful() else 1)
'''


def owning_test(path):
    """Map each implementation module to its single owning test file."""
    parent, stem = path.rsplit("/", 1)
    return f"{parent}/tests/test_{stem}"


def execute_owning_test(test_path, snapshot_dir):
    """Run one committed owning test module from the extracted tree snapshot.

    Tests are collected in-process by name without namespace packages, so a
    missing ``__init__.py`` cannot fail the import. Fails closed on nonzero
    exit, zero tests run, or any skipped test, using the RESULT summary line.
    """
    module = test_path[:-3].replace('/', '.')
    env = dict(os.environ, RECURSION_ENV='1', PYTHONDONTWRITEBYTECODE='1')
    result = subprocess.run([sys.executable, '-B', '-c', RUNNER, module],
                            cwd=snapshot_dir, capture_output=True, text=True,
                            env=env)
    match = re.search(r'^RESULT (\d+) (\d+)$', result.stdout, re.MULTILINE)
    counts = map(int, match.groups()) if match else (None, None)
    return result_failures(test_path, result, *counts)


def result_failures(test_path, result, tests_run, skipped):
    """Translate one owning-test subprocess result into failure strings."""
    detail = result.stderr.strip() or result.stdout.strip()
    if tests_run is None:
        return [f"{test_path}: owning tests produced no result line:\n{detail}"]
    return outcome_failures(test_path, result, detail, tests_run, skipped)


def outcome_failures(test_path, result, detail, tests_run, skipped):
    """Classify a parsed result: nonzero exit, zero tests or skips fail."""
    if result.returncode:
        return [f"{test_path}: owning tests failed "
                f"(exit {result.returncode}):\n{detail}"]
    if not tests_run:
        return [f"{test_path}: owning tests ran 0 tests:\n{detail}"]
    return [f"{test_path}: owning tests skipped {skipped}"] if skipped else []


def extract_snapshot(tree):
    """Materialise the committed candidate tree into a temporary directory."""
    temporary = tempfile.TemporaryDirectory(prefix='py-contract-')
    listing = [name for name in tree]
    archive = subprocess.run(['git', 'archive', '--format=tar', 'HEAD', '--', *listing],
                             capture_output=True)
    if archive.returncode != 0:
        temporary.cleanup()
        raise ValueError('cannot extract committed tree snapshot')
    import tarfile
    import io
    with tarfile.open(fileobj=io.BytesIO(archive.stdout)) as bundle:
        bundle.extractall(temporary.name, filter='data')
    return temporary


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


def collect_executions(scripts, tree):
    """Changed scripts mapped to owning tests present in the committed tree.

    Executing the checker's own owning test would recurse through synthetic
    fixtures forever, so it is excluded like in partition_changed.
    """
    _, implementations = partition_changed(scripts)
    tests = {owning_test(path) for path in implementations}
    return sorted(test for test in tests if test in tree)


def test_path_violations(path, tree):
    """Structural checks for a changed test file against its implementation."""
    owner = owning_implementation(path)
    missing = [f"{owner}: missing owning test {path}"] if owner in tree else []
    if not owner:
        layout = path.rsplit('/', 1)[-1].startswith('test_')
        return [f"{path}: unsupported owning test layout"] if layout else []
    if path not in tree:
        return missing
    if owner not in tree:
        return [f"{path}: no matching implementation {owner}"]
    return test_violations(path, tree)


def partition_changed(scripts):
    """Split changed paths into test files and contractable implementations.

    The checker's own module lands in neither: its owning test builds
    synthetic fixtures that cannot satisfy the structural contract, and the
    module is still covered by the contract suite's own CI run.
    """
    test_paths = [p for p in scripts if '/tests/' in p]
    implementations = [p for p in scripts
                       if p != SELF_MODULE and p not in test_paths
                       and not p.endswith('/__init__.py')]
    return test_paths, implementations


def static_violations(scripts, tree):
    """Structural checks for changed scripts against the committed tree."""
    failures = []
    test_paths, implementations = partition_changed(scripts)
    for path in test_paths + implementations:
        if '/tests/' in path:
            failures.extend(test_path_violations(path, tree))
            continue
        required = owning_test(path)
        if path not in tree:
            if required in tree:
                failures.append(f"{required}: no matching implementation {path}")
            continue
        if required not in tree:
            failures.append(f"{path}: missing owning test {required}")
        failures.extend(test_violations(required, tree))
        try:
            failures.extend(source_violations(path, read_blob(tree, path)))
        except (KeyError, ValueError, UnicodeError) as error:
            failures.append(f"{path}: invalid committed source: {error}")
    return failures


def execute_all(scripts, tree):
    """Run every changed script's owning test from one tree snapshot."""
    failures = []
    snapshot_dir = None
    try:
        for test_path in collect_executions(scripts, tree):
            if snapshot_dir is None:
                snapshot_dir = extract_snapshot(tree)
            failures.extend(execute_owning_test(test_path, snapshot_dir.name))
    finally:
        if snapshot_dir is not None:
            snapshot_dir.cleanup()
    return failures


def candidate_errors(base):
    """Use the committed candidate tree and an exact SHA base, never local edits."""
    tree, _ = snapshot(base, '*.py')
    scripts = sorted(filter(lambda path: path.startswith('scripts/'),
                            changed_python_paths(base)))
    return static_violations(scripts, tree) + execute_all(scripts, tree)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True, help='exact protected PR base SHA')
    args = parser.parse_args()
    try:
        failures = candidate_errors(args.base)
    except (ValueError, OSError) as error:
        failures = [str(error)]
    return report(failures)


def report(failures):
    """Print every failure to stderr; return the process exit code."""
    for failure in failures:
        print('FAIL: ' + failure, file=sys.stderr)
    return int(bool(failures))


if __name__ == '__main__':
    raise SystemExit(main())
