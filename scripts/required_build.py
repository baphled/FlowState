"""Read the deployed Build prerequisite gates for deterministic CI tests.

Tests exercise the actual workflow shell commands and job dependencies, rather
than a copy that can diverge from the required Build gate.
"""
from pathlib import Path
import re
import textwrap

ROOT = Path(__file__).resolve().parents[1]


def gates():
    for name in ('ci-next.yml', 'ci.yml'):
        source = (ROOT / '.github/workflows' / name).read_text()
        build = source.split('  build:\n', 1)[1].split('\n  security:', 1)[0]
        if 'name: Require successful prerequisites' not in build:
            if name == 'ci-next.yml':
                raise AssertionError('required next Build has no failure gate')
            continue
        if 'if: ${{ always() }}' not in build:
            raise AssertionError('required Build can skip after prerequisite failure')
        block = build.split('name: Require successful prerequisites', 1)[1]
        script = block.split('run: |\n', 1)[1].split('\n      - ', 1)[0]
        jobs = re.search(r'needs: \[([^]]+)\]', build)[1].replace(' ', '').split(',')
        yield name, jobs, textwrap.dedent(script)

