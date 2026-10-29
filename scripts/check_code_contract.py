#!/usr/bin/env python3
"""Enforce structure on changed committed production Go sources."""
import argparse
import json
import subprocess
from contract_sources import read_blob, snapshot


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True)
    args = parser.parse_args()
    tree, changed = snapshot(args.base, '*.go')
    files = [{'Name': p, 'Source': read_blob(tree, p)} for p in changed if not p.endswith('_test.go')]
    return subprocess.run(['go', 'run', './scripts/code-contract'], input=json.dumps(files).encode()).returncode


if __name__ == '__main__':
    raise SystemExit(main())
