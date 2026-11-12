"""Owning tests for the guard CLI entry point."""
import subprocess
import sys
import unittest

from scripts.project_metadata.__main__ import main


class CLI(unittest.TestCase):
    def test_changed_requires_base(self):
        with self.assertRaises(SystemExit):
            main()


if __name__ == '__main__':
    unittest.main()
