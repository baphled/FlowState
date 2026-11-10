#!/usr/bin/env python3
"""Check changed committed Python source structure and test-file ownership."""
import argparse
import ast
import sys

from scripts.contract_sources import git, read_blob, snapshot

MAX_MODULE_LINES = 250
MAX_MODULE_BRANCHES = 35
MAX_FUNCTION_LINES = 60
MAX_FUNCTION_STATEMENTS = 40
MAX_FUNCTION_COMPLEXITY = 15
BRANCH_NODES = (ast.If, ast.IfExp, ast.For, ast.AsyncFor, ast.While,
                ast.ExceptHandler, ast.Assert, ast.comprehension)
FUNCTION_NODES = (ast.FunctionDef, ast.AsyncFunctionDef)
NESTED_SCOPE_NODES = FUNCTION_NODES + (ast.Lambda, ast.ClassDef)


def branch_points(node):
    """Count conditional paths, including Boolean operands and match cases."""
    if isinstance(node, ast.BoolOp):
        return len(node.values) - 1
    if isinstance(node, ast.Match):
        return len(node.cases)
    return int(isinstance(node, BRANCH_NODES))


def lexical_function_nodes(node):
    """Walk one function body without charging nested scopes to its metrics."""
    nodes = []
    pending = list(reversed(node.body))
    while pending:
        current = pending.pop()
        nodes.append(current)
        if isinstance(current, NESTED_SCOPE_NODES):
            continue
        pending.extend(reversed(list(ast.iter_child_nodes(current))))
    return nodes


def source_violations(path, source):
    """Check a complete changed Python blob, not a selected diff hunk."""
    try:
        module = ast.parse(source, filename=path)
    except SyntaxError as error:
        return [f"{path}: invalid Python syntax at line {error.lineno}"]
    failures = []
    module_lines = len(source.splitlines())
    module_branches = sum(map(branch_points, ast.walk(module)))
    if module_lines > MAX_MODULE_LINES:
        failures.append(f"{path}: module {module_lines} lines exceeds {MAX_MODULE_LINES}")
    if module_branches > MAX_MODULE_BRANCHES:
        failures.append(f"{path}: module branches {module_branches} exceeds {MAX_MODULE_BRANCHES}")
    for node in ast.walk(module):
        if not isinstance(node, FUNCTION_NODES):
            continue
        nodes = lexical_function_nodes(node)
        lines = node.end_lineno - node.lineno + 1
        statements = sum(isinstance(item, ast.stmt) for item in nodes)
        complexity = 1 + sum(map(branch_points, nodes))
        metrics = (("lines", lines, MAX_FUNCTION_LINES),
                   ("statements", statements, MAX_FUNCTION_STATEMENTS),
                   ("complexity", complexity, MAX_FUNCTION_COMPLEXITY))
        failures.extend(f"{path}:{node.lineno} {node.name} {label} {actual} exceeds {limit}"
                        for label, actual, limit in metrics if actual > limit)
    return failures


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
                  base, 'HEAD', '--', '*.py').split(b'\\0')
    return [name.decode() for name in filter(None, changed)]


def test_violations(path, tree):
    """Require a regular Python test file with at least one test entry point."""
    try:
        source = read_blob(tree, path)
        module = ast.parse(source, filename=path)
    except (KeyError, ValueError, UnicodeError, SyntaxError) as error:
        return [f"{path}: invalid owning test: {error}"]
    tested = any(isinstance(item, FUNCTION_NODES) and item.name.startswith('test_')
                 for item in ast.walk(module))
    return [] if tested else [f"{path}: no test_* function or method present"]


def candidate_errors(base):
    """Use the committed candidate tree and an exact SHA base, never local edits."""
    tree, _ = snapshot(base, '*.py')
    scripts = sorted(path for path in changed_python_paths(base)
                     if path.startswith('scripts/'))
    failures = []
    for path in scripts:
        if '/tests/' in path:
            owner = owning_implementation(path)
            if not owner:
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
