"""Unit tests for Python contract AST policy."""
import ast
import unittest

from scripts.python_contract_ast import discoverable_test, source_violations

GOOD = 'def task():\n    return 1\n'


class SourceContract(unittest.TestCase):
    def test_focused_function_is_accepted(self):
        self.assertEqual(source_violations('scripts/task.py', GOOD), [])

    def test_large_function_is_rejected(self):
        source = 'def task():\n' + '    pass\n' * 60
        self.assertTrue(any('lines' in x for x in source_violations('scripts/task.py', source)))

    def test_excessive_statements_are_rejected(self):
        source = 'def task():\n' + '    pass\n' * 41
        self.assertTrue(any('statements' in x for x in source_violations('scripts/task.py', source)))

    def test_conditional_complexity_is_rejected(self):
        source = 'def task():\n' + '    if True: pass\n' * 16
        self.assertTrue(any('complexity' in x for x in source_violations('scripts/task.py', source)))

    def test_nested_function_complexity_is_not_charged_to_outer(self):
        outer = ['def task():'] + ['    if True: pass'] * 8
        outer += ['    def inner():'] + ['        if True: pass'] * 8
        self.assertEqual(source_violations('scripts/task.py', '\n'.join(outer) + '\n'), [])

    def test_nested_function_lines_are_not_charged_to_outer(self):
        source = ['def task():', '    def inner(',
                  '            first,', '            second):', '        value = (']
        source += ["            'x'"] * 54
        source += ['        )', '        return value', '    return 1']
        self.assertEqual(source_violations('scripts/task.py', '\n'.join(source) + '\n'), [])

    def test_lambda_complexity_is_charged_to_outer(self):
        expr = 'value'
        for _ in range(15):
            expr = f'value if value else ({expr})'
        source = f'def task(value):\n    return lambda value: {expr}\n'
        self.assertTrue(any('complexity' in x for x in source_violations('scripts/task.py', source)))

    def test_nested_class_body_complexity_is_charged_to_outer(self):
        source = 'def task():\n    class Local:\n' + '        if True: pass\n' * 15
        self.assertTrue(any('complexity' in x for x in source_violations('scripts/task.py', source)))

    def test_large_or_branchy_module_is_rejected(self):
        module = GOOD + '# blank\n' * 250
        self.assertTrue(any('module' in x for x in source_violations('scripts/task.py', module)))
        branchy = 'if True: pass\n' * 36
        self.assertTrue(any('module branches' in x for x in source_violations('scripts/task.py', branchy)))

    def test_inline_waiver_does_not_exempt_complexity(self):
        source = 'def task(): # noqa: complexity\n' + '    if True: pass\n' * 16
        self.assertTrue(source_violations('scripts/task.py', source))

    def test_invalid_syntax_fails(self):
        self.assertTrue(any('invalid Python syntax' in x
                            for x in source_violations('scripts/task.py', 'def :')))


class TestDiscovery(unittest.TestCase):
    def test_module_function_is_discoverable(self):
        self.assertTrue(discoverable_test(ast.parse('def test_task():\n    pass\n')))

    def test_nested_function_is_not_discoverable(self):
        module = ast.parse('def helper():\n    def test_fake():\n        pass\n')
        self.assertFalse(discoverable_test(module))

    def test_testcase_method_is_discoverable(self):
        module = ast.parse('class Checks(unittest.TestCase):\n    def test_task(self):\n        pass\n')
        self.assertTrue(discoverable_test(module))


class SkipOnlyTests(unittest.TestCase):
    def test_module_level_skip_only_test_is_not_effective(self):
        source = ('import unittest\n'
                  '@unittest.skip("none")\n'
                  'def test_task():\n'
                  '    assert True\n')
        self.assertFalse(discoverable_test(ast.parse(source)))

    def test_skipif_guarded_test_is_not_effective(self):
        source = ('import unittest\n'
                  '@unittest.skipIf(True, "none")\n'
                  'def test_task():\n'
                  '    assert True\n')
        self.assertFalse(discoverable_test(ast.parse(source)))

    def test_testcase_skip_class_is_not_effective(self):
        source = ('import unittest\n'
                  '@unittest.skip("none")\n'
                  'class Checks(unittest.TestCase):\n'
                  '    def test_task(self):\n'
                  '        self.assertTrue(True)\n')
        self.assertFalse(discoverable_test(ast.parse(source)))

    def test_expected_failure_test_is_not_effective(self):
        source = ('import unittest\n'
                  '@unittest.expectedFailure\n'
                  'def test_task():\n'
                  '    assert True\n')
        self.assertFalse(discoverable_test(ast.parse(source)))

    def test_skip_by_name_alias_is_not_effective(self):
        source = ('from unittest import skip as s\n'
                  '@s("none")\n'
                  'def test_task():\n'
                  '    assert True\n')
        self.assertFalse(discoverable_test(ast.parse(source)))

    def test_unrelated_decorator_does_not_disable_test(self):
        source = ('import functools\n'
                  '@functools.cache\n'
                  'def test_task():\n'
                  '    assert True\n')
        self.assertTrue(discoverable_test(ast.parse(source)))

    def test_plain_test_in_skip_class_still_effective(self):
        source = ('import unittest\n'
                  'class Checks(unittest.TestCase):\n'
                  '    def test_task(self):\n'
                  '        self.assertTrue(True)\n')
        self.assertTrue(discoverable_test(ast.parse(source)))


if __name__ == '__main__':
    unittest.main()
