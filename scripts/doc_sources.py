#!/usr/bin/env python3
"""Export committed document inputs without following working-copy links."""
import argparse
import json
from contract_sources import read_blob, snapshot


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--base', required=True)
args = parser.parse_args()
tree, changed = snapshot(args.base, '*')
print(json.dumps({'documents': {p: read_blob(tree, p) for p in tree if p.lower().endswith('.md')},
                  'files': {p: mode.decode() for p, (mode, _, _) in tree.items()},
                  'changed': changed}))
