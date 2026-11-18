#!/usr/bin/env python3
"""Reject project run records in Git; keep only a neutral Dropbox reference.

Thin compatibility entry point: the implementation lives in the
scripts/project_metadata package (decomposed for the Python source
contract); running this module delegates to that CLI unchanged.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from scripts.project_metadata.__main__ import main

if __name__ == '__main__':
    sys.exit(main())
