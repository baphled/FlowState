#!/usr/bin/env python3
"""Compare complete gosec scans of immutable Git candidates; never waive new findings."""
import argparse
from collections import Counter
import json
from pathlib import Path
import re
import shutil
import subprocess
import tempfile


def git(repo, *args):
    """Read repository objects, raising on missing revisions or Git failures."""
    return subprocess.check_output(['git', '-C', str(repo), *args], stderr=subprocess.PIPE)


def snapshot(repo, revision, destination):
    """Materialize every tracked regular blob, independent of archive attributes."""
    destination.mkdir()
    entries = git(repo, 'ls-tree', '-rz', '--full-tree', revision).split(b'\0')
    for entry in entries:
        if not entry:
            continue
        metadata, raw_path = entry.split(b'\t', 1)
        mode, kind, object_id = metadata.decode('ascii').split(' ')
        path = Path(raw_path.decode('utf-8', errors='strict'))
        if (path.is_absolute() or not path.parts or
                any(part in ('.', '..') or part.casefold() == '.git' for part in path.parts) or
                path.as_posix().encode('utf-8') != raw_path):
            raise ValueError('unsafe tracked snapshot path')
        if kind != 'blob' or mode not in ('100644', '100755'):
            raise ValueError(f'unsupported tracked snapshot mode: {mode} {path}')
        target = destination / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(git(repo, 'cat-file', 'blob', object_id))
        target.chmod(0o755 if mode == '100755' else 0o644)


def fingerprint(issue, root):
    """Identify findings independently of line shifts while retaining multiplicity."""
    path = Path(issue['file'])
    relative = path.resolve().relative_to(root.resolve()).as_posix()
    if issue['severity'] not in ('LOW', 'MEDIUM', 'HIGH'):
        raise ValueError('invalid severity')
    if issue['confidence'] not in ('LOW', 'MEDIUM', 'HIGH'):
        raise ValueError('invalid confidence')
    line_range = issue['line'].split('-')
    start, end = int(line_range[0]), int(line_range[-1])
    code = []
    for line in issue['code'].splitlines():
        match = re.match(r'^(\d+):\s?(.*)$', line)
        if match and start <= int(match[1]) <= end:
            code.append(match[2].strip())
    if not code or not issue['rule_id'] or not issue['details']:
        raise ValueError('incomplete finding')
    return (relative, issue['rule_id'], issue['details'], '\n'.join(code),
            issue['severity'], issue['confidence'])


def scan(scanner, root, output, label):
    """Retain native JSON/SARIF and reject scanner, import and report failures."""
    findings = None
    for fmt in ('json', 'sarif'):
        report = output / f'{label}.{fmt}'
        result = subprocess.run([scanner, '-nosec=true', '-fmt', fmt, '-out', str(report), './...'],
                                cwd=root, capture_output=True, text=True, timeout=300)
        (output / f'{label}.{fmt}.stdout').write_text(result.stdout)
        (output / f'{label}.{fmt}.stderr').write_text(result.stderr)
        (output / f'{label}.{fmt}.exit').write_text(str(result.returncode) + '\n')
        if result.returncode not in (0, 1):
            raise ValueError(f'{label}: scanner {fmt} exit {result.returncode}')
        data = json.loads(report.read_text())
        if fmt == 'json':
            if data.get('Golang errors') != {} or 'Issues' not in data:
                raise ValueError(f'{label}: scanner/import errors or invalid JSON report')
            issues = data['Issues']
            if issues is None:
                issues = []
            if not isinstance(issues, list) or data['Stats']['files'] <= 0:
                raise ValueError(f'{label}: no scanned files or invalid issues')
            findings = Counter(fingerprint(issue, root) for issue in issues)
            if data['Stats']['found'] != sum(findings.values()):
                raise ValueError(f'{label}: inconsistent finding count')
        else:
            runs = data['runs']
            if len(runs) != 1 or 'tool' not in runs[0]:
                raise ValueError(f'{label}: invalid SARIF report')
            for invocation in runs[0].get('invocations', []):
                if invocation.get('executionSuccessful') is False:
                    raise ValueError(f'{label}: SARIF execution failed')
                if invocation.get('toolExecutionNotifications'):
                    raise ValueError(f'{label}: SARIF tool notifications')
            if len(runs[0].get('results', [])) != sum(findings.values()):
                raise ValueError(f'{label}: JSON/SARIF scans disagree')
        if result.returncode != (1 if findings else 0):
            raise ValueError(f'{label}: inconsistent scanner exit/report')
    return findings


def describe(label, findings):
    """Print debt and regression totals without claiming findings are exploitable."""
    rules = Counter()
    severities = Counter()
    for finding, count in findings.items():
        rules[finding[1]] += count
        severities[finding[4]] += count
    print(f'{label}: {sum(findings.values())}; rules={dict(sorted(rules.items()))}; '
          f'severity={dict(sorted(severities.items()))}')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True, help='full immutable PR base or push before SHA')
    parser.add_argument('--repo', type=Path, default=Path.cwd())
    parser.add_argument('--scanner', default='gosec')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    try:
        if not re.fullmatch(r'[0-9a-f]{40}', args.base) or args.base == '0' * 40:
            raise ValueError('base must be a nonzero full commit SHA')
        base = git(args.repo, 'rev-parse', '--verify', args.base + '^{commit}').decode().strip()
        head = git(args.repo, 'rev-parse', '--verify', 'HEAD^{commit}').decode().strip()
        git(args.repo, 'merge-base', '--is-ancestor', base, head)
        scanner = shutil.which(args.scanner)
        if scanner is None:
            raise ValueError('scanner executable unavailable')
        scanner = str(Path(scanner).resolve())
        output = args.output.resolve()
        worktree = Path(git(args.repo, 'rev-parse', '--show-toplevel').decode().strip()).resolve()
        common = Path(git(args.repo, 'rev-parse', '--path-format=absolute', '--git-common-dir').decode().strip()).resolve()
        if output.is_relative_to(worktree) or output.is_relative_to(common):
            raise ValueError('report output must be outside the Git worktree and common directory')
        output.mkdir(parents=True, exist_ok=False)
        print(f'Immutable base={base} candidate={head}')
        with tempfile.TemporaryDirectory(prefix='flowstate-security-') as temporary:
            root = Path(temporary)
            snapshot(args.repo, base, root / 'base')
            snapshot(args.repo, head, root / 'candidate')
            reports = {}
            failures = []
            for label in ('base', 'candidate'):
                try:
                    reports[label] = scan(scanner, root / label, output, label)
                except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
                    failures.append(str(error))
            if failures:
                raise ValueError('; '.join(failures))
            baseline, candidate = reports['base'], reports['candidate']
            inherited = baseline & candidate
            new = candidate - baseline
            describe('Inherited findings (unresolved debt, not a security clearance)', inherited)
            describe('New findings', new)
            for finding, count in sorted(new.items()):
                print(f'NEW x{count}: {finding}')
            return 1 if new else 0
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print(f'Security gate failed closed: {error}')
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
