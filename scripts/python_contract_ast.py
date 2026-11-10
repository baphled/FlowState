"""AST policy for the Python source-quality contract."""
import ast

MAX_MODULE_LINES = 250
MAX_MODULE_BRANCHES = 35
MAX_FUNCTION_LINES = 60
MAX_FUNCTION_STATEMENTS = 40
MAX_FUNCTION_COMPLEXITY = 15
BRANCH_NODES = (ast.If, ast.IfExp, ast.For, ast.AsyncFor, ast.While,
                ast.ExceptHandler, ast.Assert, ast.comprehension)
FUNCTION_NODES = (ast.FunctionDef, ast.AsyncFunctionDef)


def branch_points(node):
    """Count conditional paths, including Boolean operands and match cases."""
    if isinstance(node, ast.BoolOp):
        return len(node.values) - 1
    if isinstance(node, ast.Match):
        return len(node.cases)
    return int(isinstance(node, BRANCH_NODES))


def lexical_function_nodes(node):
    """Walk lexical work while excluding only nested function bodies."""
    nodes = []
    pending = list(reversed(node.body))
    while pending:
        current = pending.pop()
        nodes.append(current)
        if isinstance(current, FUNCTION_NODES):
            children = []
            for field, value in ast.iter_fields(current):
                if field == 'body':
                    continue
                if isinstance(value, ast.AST):
                    children.append(value)
                elif isinstance(value, list):
                    children.extend(filter(lambda item: isinstance(item, ast.AST), value))
            pending.extend(reversed(children))
            continue
        pending.extend(reversed(list(ast.iter_child_nodes(current))))
    return nodes


def lexical_function_lines(node):
    """Count source lines while excluding nested function body statements."""
    lines = set(range(node.lineno, node.end_lineno + 1))
    for nested in ast.walk(node):
        if nested is node or not isinstance(nested, FUNCTION_NODES):
            continue
        for statement in nested.body:
            lines.difference_update(range(statement.lineno, statement.end_lineno + 1))
    return len(lines)


def source_violations(path, source):
    """Check source size and complexity without diff-hunk exemptions."""
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
        metrics = (("lines", lexical_function_lines(node), MAX_FUNCTION_LINES),
                   ("statements", sum(isinstance(item, ast.stmt) for item in nodes),
                    MAX_FUNCTION_STATEMENTS),
                   ("complexity", 1 + sum(map(branch_points, nodes)),
                    MAX_FUNCTION_COMPLEXITY))
        failures.extend(f"{path}:{node.lineno} {node.name} {label} {actual} exceeds {limit}"
                        for label, actual, limit in metrics if actual > limit)
    return failures


def discoverable_test(module):
    """Recognise module-level pytest functions or conventional test methods."""
    for item in module.body:
        if isinstance(item, FUNCTION_NODES) and item.name.startswith('test_'):
            return True
        if not isinstance(item, ast.ClassDef):
            continue
        unittest_class = any(
            (isinstance(base, ast.Name) and base.id == 'TestCase')
            or (isinstance(base, ast.Attribute) and base.attr == 'TestCase')
            for base in item.bases
        )
        if not (item.name.startswith('Test') or unittest_class):
            continue
        if any(isinstance(member, FUNCTION_NODES) and member.name.startswith('test_')
               for member in item.body):
            return True
    return False
