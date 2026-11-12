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
SKIP_NAMES = {"skip", "skipIf", "skipUnless", "expectedFailure"}


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
        children = _ast_children(current) if isinstance(current, FUNCTION_NODES) \
            else list(ast.iter_child_nodes(current))
        pending.extend(reversed(children))
    return nodes


def _ast_items(value):
    """AST values inside a field value, wrapping single nodes in a list."""
    return list(filter(_is_ast, _as_list(value)))


def _as_list(value):
    """Wrap a non-list field value in a single-element list."""
    return value if isinstance(value, list) else [value]


def _is_ast(item):
    """Whether the item is an AST node."""
    return isinstance(item, ast.AST)


def _ast_children(node):
    """Child nodes of a function outside its body: signature and decorators."""
    children = []
    for field, value in ast.iter_fields(node):
        if field != 'body':
            children.extend(_ast_items(value))
    return children


def _statement_lines(statements):
    """Source lines occupied by a list of statements."""
    return set().union(*[range(s.lineno, s.end_lineno + 1) for s in statements])


def _nested_functions(node):
    """Every function nested inside node, excluding node itself."""
    return [n for n in ast.walk(node) if isinstance(n, FUNCTION_NODES)][1:]


def _nested_body_lines(node):
    """Source lines occupied by nested function bodies inside node."""
    bodies = map(_statement_lines, [n.body for n in _nested_functions(node)])
    return set().union(*bodies)



def lexical_function_lines(node):
    """Count source lines while excluding nested function body statements."""
    own = range(node.lineno, node.end_lineno + 1)
    return len(set(own) - _nested_body_lines(node))


def source_violations(path, source):
    """Check source size and complexity without diff-hunk exemptions."""
    try:
        module = ast.parse(source, filename=path)
    except SyntaxError as error:
        return [f"{path}: invalid Python syntax at line {error.lineno}"]
    failures = module_violations(path, source, module)
    failures.extend(function_failures(path, module))
    return failures


def function_failures(path, module):
    """Per-function violations for every sync/async function in the module."""
    functions = _module_functions(module)
    failures = []
    for node in functions:
        failures.extend(function_violations(path, node))
    return failures


def _module_functions(module):
    """Every sync/async function definition in the module."""
    return [node for node in ast.walk(module) if isinstance(node, FUNCTION_NODES)]


def module_violations(path, source, module):
    """Whole-module size and conditional-complexity bounds."""
    failures = []
    module_lines = len(source.splitlines())
    if module_lines > MAX_MODULE_LINES:
        failures.append(f"{path}: module {module_lines} lines exceeds {MAX_MODULE_LINES}")
    failures.extend(_branch_failures(path, module))
    return failures


def _branch_failures(path, module):
    """Failure line when the module's total branch points exceed the bound."""
    branches = sum(map(branch_points, ast.walk(module)))
    return [] if branches <= MAX_MODULE_BRANCHES else [
        f"{path}: module branches {branches} exceeds {MAX_MODULE_BRANCHES}"]


def function_violations(path, node):
    """Per-function lexical line, statement and complexity bounds."""
    nodes = lexical_function_nodes(node)
    metrics = (("lines", lexical_function_lines(node), MAX_FUNCTION_LINES),
               ("statements", _statement_count(nodes), MAX_FUNCTION_STATEMENTS),
               ("complexity", 1 + sum(map(branch_points, nodes)),
                MAX_FUNCTION_COMPLEXITY))
    return _metric_failures(path, node, metrics)


def _statement_count(nodes):
    """Lexical AST statements charged to one function scope."""
    return sum(isinstance(item, ast.stmt) for item in nodes)


def _metric_failures(path, node, metrics):
    """Failure lines for each metric that exceeds its limit."""
    return [f"{path}:{node.lineno} {node.name} {label} {actual} exceeds {limit}"
            for label, actual, limit in metrics if actual > limit]


def _imported_skip_aliases(item):
    """Bare names this import binds to unittest's skip family."""
    return list(map(_bound_name, _skip_family_aliases(item)))


def _skip_family_aliases(item):
    """Imported aliases whose imported name is in the skip family."""
    return [alias for alias in item.names
            if alias.name.split('.')[-1] in SKIP_NAMES]


def _bound_name(alias):
    """The bare name an import alias binds in the module."""
    return alias.asname or alias.name


def _import_statements(module):
    """Import statements at module level."""
    return [i for i in module.body if isinstance(i, (ast.Import, ast.ImportFrom))]


def _skip_names(module):
    """Every bare name bound to unittest's skip family anywhere in the module."""
    skips = set(SKIP_NAMES)
    for item in _import_statements(module):
        skips.update(_imported_skip_aliases(item))
    return skips


def _decorator_name(decorator):
    """Attribute or bare name of a decorator, e.g. skip or skipIf(...)."""
    target = decorator.func if isinstance(decorator, ast.Call) else decorator
    return getattr(target, 'attr', None) or getattr(target, 'id', None)


def _is_disabled(item, skips):
    """A skip-family decorator (attribute or alias) on a test or its class."""
    return any(_decorator_name(d) in skips for d in getattr(item, 'decorator_list', []))


def _base_name(base):
    """The plain or dotted name a class inherits from."""
    return base.id if isinstance(base, ast.Name) else getattr(base, 'attr', None)


def _is_test_case_class(item):
    """A module-level Test* named class or an explicit TestCase subclass."""
    if not isinstance(item, ast.ClassDef):
        return False
    return item.name.startswith('Test') or _inherits_test_case(item)


def _inherits_test_case(item):
    """Whether any base of the class is named TestCase."""
    return any(_base_name(base) == 'TestCase' for base in item.bases)


def _is_test_named_function(item):
    """A sync/async function whose name follows the test_ convention."""
    return isinstance(item, FUNCTION_NODES) and item.name.startswith('test_')


def _is_effective_test_function(item, skips):
    """A test-named function not disabled by a skip-family decorator."""
    return _is_test_named_function(item) and not _is_disabled(item, skips)


def _effective_class_test(item, skips):
    """An enabled test method on an enabled Test*/TestCase-style class."""
    if not _is_test_case_class(item) or _is_disabled(item, skips):
        return False
    return _effective_member_test(item.body, skips)


def _effective_member_test(members, skips):
    """Whether any class member is an enabled test method."""
    return any(_is_effective_test_function(member, skips) for member in members)


def _has_effective_test(item, skips):
    """One effective test entry: enabled test function or class method."""
    if isinstance(item, FUNCTION_NODES):
        return _is_effective_test_function(item, skips)
    return _effective_class_test(item, skips)


def discoverable_test(module):
    """Recognise an effective (not skipped/expected-failure) test entry point.

    A module-level pytest function or a test method on a module-level
    Test*/TestCase-style class counts only when it is not disabled by a
    skip-family decorator on itself or its class. A file whose only tests
    are skipped or expected failures owns no effective coverage.
    """
    skips = _skip_names(module)
    return any(_has_effective_test(item, skips) for item in module.body)
