#!/usr/bin/env python3
"""Reject project run records in Git; keep only a neutral Dropbox reference."""

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path, PurePosixPath


REFERENCE = re.compile(r'dropbox:/FlowState/Metadata/attempts/[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*\.json')
# Identity and review claims belong only in the external record.
CLAIM = re.compile(r'^\s*(?:AI-Generated-By|Reviewed-By|AI-Agent|AI-Model|Co-authored-by)\s*:', re.I | re.M)
RECORD_PATH = re.compile(r'(^|/)(?:receipts?|qualifications?|evidence|reports?|agent-metadata|project-metadata|tasks?|plans?|handoffs?|progress)(?:/|[._-])|(?:^|/)[^/]*(?:qualification|receipt|attestation|validation-evidence|review-result)[^/]*\.(?:md|json|ya?ml|txt|log|zip|db|sqlite)$', re.I)
LEGACY_RECORD = re.compile(r'(^|/)(?:HANDOFF|PROGRESS|PLAN)\.md$', re.I)
CODE_SUFFIXES = {'.go', '.py', '.sh', '.js', '.ts', '.tsx', '.vue'}


def git(*args, cwd=None):
    return subprocess.check_output(['git', *args], cwd=cwd)


def message_errors(message):
    errors = []
    if CLAIM.search(message):
        errors.append('identity/review trailers belong in Dropbox, not commit messages')
    references = re.findall(r'^Project-Metadata: (.+)$', message, re.M)
    if len(references) != 1 or not REFERENCE.fullmatch(references[0]):
        errors.append('one valid Project-Metadata: dropbox:/FlowState/Metadata/attempts/<attempt>/<record>.json is required')
    return errors


def record_errors(path, content):
    item = PurePosixPath(path)
    if item.suffix == '.feature':
        return ['Cucumber/Gherkin feature files are forbidden: ' + path]
    if item.name in ('go.mod', 'go.sum') or item.suffix == '.go':
        if re.search(rb'github\.com/(?:cucumber/|DATA-DOG/godog)', content, re.I):
            return ['Cucumber/Godog dependencies and imports are forbidden: ' + path]
    if '.github/workflows/' in path and item.suffix in ('.yml', '.yaml'):
        if re.search(rb'(?:\bgodog\b|\bcucumber-js\b|make[ \t]+bdd(?:[-\s;]|$))', content, re.I):
            return ['Cucumber/Godog workflow execution is forbidden: ' + path]
    if item.suffix in CODE_SUFFIXES or item.name.endswith('.schema.json'):
        return []
    value = None
    if item.suffix == '.json':
        try:
            value = json.loads(content)
        except (ValueError, UnicodeError):
            pass
    if {'testdata', 'fixtures'} & set(item.parts) and isinstance(value, dict) and value.get('synthetic_fixture') is True:
        return []
    if RECORD_PATH.search(path) or LEGACY_RECORD.search(path):
        return ['project metadata file must be stored in Dropbox: ' + path]
    if item.suffix.lower() in {'.md', '.txt', '.log'}:
        references = re.findall(r'^\s*Project-Metadata:\s*(\S+)\s*$',
                                content.decode('utf-8', 'replace'), re.M)
        if any(REFERENCE.fullmatch(reference) for reference in references):
            return ['executed delivery record must be stored in Dropbox: ' + path]
    if isinstance(value, dict):
        version = str(value.get('schema_version', value.get('record_type', '')))
        if re.search(r'(?:acceptance-receipt|work-packet|qualification|project-metadata)', version):
            return ['project metadata content must be stored in Dropbox: ' + path]
        if {'gate_outcome', 'repo_state', 'evidence'} <= value.keys():
            return ['executed evidence receipt must be stored in Dropbox: ' + path]
    return []


def directory_errors(root):
    errors = []
    for item in sorted(Path(root).rglob('*')):
        if item.is_file():
            errors.extend(record_errors(item.relative_to(root).as_posix(), item.read_bytes()))
    return errors


def tree_errors(tree=None):
    if tree:
        rows = git('ls-tree', '-r', '-z', tree).split(b'\0')
    else:
        rows = git('ls-files', '--stage', '-z').split(b'\0')
    errors = []
    for row in filter(None, rows):
        attributes, name = row.split(b'\t', 1)
        fields = attributes.decode().split()
        if not tree and fields[2] != '0':
            errors.append('unmerged index entry: ' + name.decode())
            continue
        oid = fields[2] if tree else fields[1]
        if tree and fields[1] != 'blob':
            continue
        path = name.decode('utf-8', 'surrogateescape')
        content = git('cat-file', 'blob', oid)
        errors.extend(record_errors(path, content))
    return errors


def changed_tree_errors(base, head):
    """Inspect every touched blob, including files subsequently deleted."""
    errors = []
    for commit in git('rev-list', f'{base}..{head}').decode().splitlines():
        names = git('diff-tree', '--root', '-m', '--no-commit-id', '--name-only',
                    '--diff-filter=ACMT', '--no-renames', '-r', '-z', commit).split(b'\0')
        for name in filter(None, names):
            path = name.decode('utf-8', 'surrogateescape')
            content = git('show', f'{commit}:{path}')
            errors.extend(f'{commit[:12]}: {e}' for e in record_errors(path, content))
    return errors


def prepare_message(source, record, output):
    """Validate a fetched external cache and bind its reference to the message.

    This offline check cannot authenticate Dropbox or the claimed observations.
    The caller must fetch the real record; source text is never modified.
    """
    errors = external_record_errors(record)
    if errors:
        return errors
    if output.resolve() in (source.resolve(), record.resolve()):
        return ['output must differ from the source message and external record']
    value = json.loads(record.read_text())
    message = source.read_text().rstrip()
    if not message or message == '...':
        errors.append('a substantive commit message is required')
    references = re.findall(r'^Project-Metadata: (.+)$', message, re.M)
    if references and references != [value.get('metadata_ref')]:
        errors.append('message reference differs from the validated external record')
    if not references:
        message += '\n\nProject-Metadata: ' + str(value.get('metadata_ref', ''))
    errors.extend(message_errors(message))
    if not errors:
        output.write_text(message + '\n')
    return errors


def external_record_errors(path):
    root = Path(git('rev-parse', '--show-toplevel').decode().strip()).resolve()
    if path.resolve().is_relative_to(root):
        return ['metadata record/cache must be outside the code checkout']
    value = json.loads(path.read_text())
    if not isinstance(value, dict):
        return ['external record must be a JSON object']
    errors = []
    if value.get('record_type') != 'flowstate-project-metadata-v1' or not REFERENCE.fullmatch(str(value.get('metadata_ref', ''))):
        errors.append('invalid external project metadata record/reference')
    if value.get('base_sha') != git('rev-parse', 'HEAD').decode().strip():
        errors.append('external record base differs from current HEAD')
    required = {'/FlowState/Dev/00-AGENT-READ-FIRST.md', '/FlowState/Dev/AGENTS.md',
                '/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md',
                '/FlowState/Metadata/ACTIVE.json'}
    reads = value.get('dropbox_reads', [])
    if not isinstance(reads, list):
        return ['dropbox_reads must be a list']
    observed = {re.sub(r'^ns:[0-9]+//', '/', str(r.get('path'))) for r in reads if isinstance(r, dict) and
                re.fullmatch('[0-9a-f]{64}', str(r.get('sha256', ''))) and r.get('file_id')}
    if not required <= observed:
        errors.append('current Dropbox entry/rules/decisions/ACTIVE reads and hashes are required')
    if not re.fullmatch('[0-9a-f]{64}', str(value.get('docs_pack_manifest_sha256', ''))):
        errors.append('current pack manifest digest is required')
    attribution = value.get('attribution', {})
    if not isinstance(attribution, dict) or not all(isinstance(attribution.get(k), str) and attribution[k].strip() for k in ('agent', 'provider', 'model', 'harness', 'source')):
        errors.append('observed attribution or explicit unverified values and source are required')
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base')
    parser.add_argument('--head', default='HEAD')
    parser.add_argument('--staged', action='store_true')
    parser.add_argument('--changed', action='store_true', help='check touched blobs in every PR commit; requires --base')
    parser.add_argument('--prepare-message', type=Path, help='prepare a neutral commit message from a fetched external record')
    parser.add_argument('--output', type=Path)
    parser.add_argument('--message-file', type=Path)
    parser.add_argument('--external-record', type=Path)
    parser.add_argument('--receipt', type=Path,
                        help='validate an external attempt receipt against the schema')
    parser.add_argument('--tree-root', type=Path,
                        help='check a plain directory tree instead of Git state')
    args = parser.parse_args()
    if args.receipt:
        return validate_receipt(RECEIPT_SCHEMA, args.receipt)
    try:
        if args.prepare_message:
            if not args.external_record or not args.output:
                parser.error('--prepare-message requires --external-record and --output')
            errors = prepare_message(args.prepare_message, args.external_record, args.output)
            for error in errors:
                print('FAIL: ' + error, file=sys.stderr)
            return bool(errors)
        if args.changed and not args.base:
            parser.error('--changed requires --base')
        if args.message_file and not (args.tree_root or args.base or args.staged or args.external_record):
            # Message-only mode: validate the commit message alone, with no
            # Git tree/context scan, so `--message-file` can fail loudly on a
            # bad message even while the checked-out tree is not yet clean.
            errors = message_errors(args.message_file.read_text())
            for error in errors:
                print('FAIL: ' + error, file=sys.stderr)
            if not errors:
                print('PASS: commit message carries only a neutral Dropbox reference')
                print('LIMIT: offline checks cannot authenticate Dropbox reads, serving identity or review')
            return bool(errors)
        if args.tree_root:
            errors = directory_errors(args.tree_root)
            for error in errors:
                print('FAIL: ' + error, file=sys.stderr)
            if not errors:
                print('PASS: no project run records in the checked tree')
                print('LIMIT: offline checks cannot authenticate Dropbox reads, serving identity or review')
            return bool(errors)
        errors = changed_tree_errors(args.base, args.head) if args.changed else tree_errors(None if args.staged or not args.base else args.head)
        if args.base:
            commits = git('rev-list', f'{args.base}..{args.head}').decode().splitlines()
            for commit in commits:
                if not args.changed:
                    errors.extend(f'{commit[:12]}: {e}' for e in tree_errors(commit))
                errors.extend(f'{commit[:12]}: {e}' for e in message_errors(git('show', '-s', '--format=%B', commit).decode()))
        if args.message_file:
            errors.extend(message_errors(args.message_file.read_text()))
        if args.external_record:
            errors.extend(external_record_errors(args.external_record))
        for error in errors:
            print('FAIL: ' + error, file=sys.stderr)
        if not errors:
            print('PASS: checked blobs and commit references satisfy metadata policy')
            print('LIMIT: offline checks cannot authenticate Dropbox reads, serving identity or review')
        return bool(errors)
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print('FAIL: ' + str(error), file=sys.stderr)
        return 1


RECEIPT_SCHEMA = {
    '$schema': 'https://json-schema.org/draft/2020-12/schema',
    'title': 'FlowState attempt record',
    'type': 'object',
    'required': ['record_type', 'metadata_ref', 'dropbox_reads',
                 'docs_pack_manifest_sha256', 'attribution'],
    'properties': {
        'record_type': {'const': 'flowstate-project-metadata-v1'},
        'metadata_ref': {
            'type': 'string',
            'pattern': r'^dropbox:/FlowState/Metadata/attempts/'
                       r'[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*\.json$'},
        'base_sha': {'type': 'string', 'pattern': '^[0-9a-f]{40,64}$'},
        'dropbox_reads': {
            'type': 'array',
            'minItems': 1,
            'items': {
                'type': 'object',
                'required': ['path', 'sha256', 'file_id'],
                'properties': {
                    'path': {'type': 'string', 'pattern': '^/FlowState/'},
                    'sha256': {'type': 'string', 'pattern': '^[0-9a-f]{64}$'},
                    'file_id': {'type': ['integer', 'string']},
                }}},
        'docs_pack_manifest_sha256': {'type': 'string', 'pattern': '^[0-9a-f]{64}$'},
        'attribution': {
            'type': 'object',
            'required': ['agent', 'provider', 'model', 'harness', 'source'],
            'properties': {
                'agent': {'type': 'string', 'minLength': 1},
                'provider': {'type': 'string', 'minLength': 1},
                'model': {'type': 'string', 'minLength': 1},
                'harness': {'type': 'string', 'minLength': 1},
                'source': {'type': 'string', 'minLength': 1},
            }},
    },
}


def validate_receipt(schema, path):
    value = json.loads(Path(path).read_text())
    try:
        import jsonschema
    except ImportError:
        # Dependency-free structural validation.
        def check():
            for key in schema['required']:
                if key not in value:
                    yield 'missing required key: ' + key
            if value.get('record_type') != 'flowstate-project-metadata-v1':
                yield 'record_type must be flowstate-project-metadata-v1'
            if not REFERENCE.fullmatch(str(value.get('metadata_ref', ''))):
                yield 'metadata_ref must be a valid Dropbox attempt reference'
            if not re.fullmatch('[0-9a-f]{64}', str(value.get('docs_pack_manifest_sha256', ''))):
                yield 'docs_pack_manifest_sha256 must be a 64-hex digest'
            attribution = value.get('attribution')
            if not isinstance(attribution, dict) or not all(
                    attribution.get(k) for k in ('agent', 'provider', 'model', 'harness', 'source')):
                yield 'attribution requires agent/provider/model/harness/source'
            for read in value.get('dropbox_reads', []):
                if not re.fullmatch('[0-9a-f]{64}', str(read.get('sha256', ''))):
                    yield 'dropbox_read sha256 must be 64-hex: ' + str(read.get('path'))
    else:
        def check():
            yield from ('%s: %s' % (list(e.absolute_path)[0] if e.absolute_path else '<root>', e.message)
                        for e in jsonschema.Draft202012Validator(schema).iter_errors(value))
    errors = list(check())
    for error in errors:
        print('FAIL: ' + error, file=sys.stderr)
    if not errors:
        print('PASS: receipt conforms to flowstate-project-metadata-v1 schema')
    return bool(errors)


if __name__ == '__main__':
    sys.exit(main())
