"""Git tree/range walks and external attempt-record validation."""
import json
import re
import subprocess
from pathlib import Path

from scripts.project_metadata import REFERENCE, message_errors, record_errors

REQUIRED_READS = {'/FlowState/Dev/00-AGENT-READ-FIRST.md', '/FlowState/Dev/AGENTS.md',
                  '/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md',
                  '/FlowState/Metadata/ACTIVE.json'}
ATTRIBUTION_KEYS = ('agent', 'provider', 'model', 'harness', 'source')
DIGEST = re.compile(r'[0-9a-f]{64}')


def git(*args):
    return subprocess.check_output(['git', *args])


def index_errors():
    """Check the staged index, rejecting unmerged entries."""
    errors = []
    for row in filter(None, git('ls-files', '--stage', '-z').split(b'\0')):
        attributes, name = row.split(b'\t', 1)
        fields = attributes.decode().split()
        if fields[2] != '0':
            errors.append('unmerged index entry: ' + name.decode())
            continue
        path = name.decode('utf-8', 'surrogateescape')
        errors.extend(record_errors(path, git('cat-file', 'blob', fields[1])))
    return errors


def committed_tree_errors(tree):
    """Check every blob of a committed tree."""
    errors = []
    for row in filter(None, git('ls-tree', '-r', '-z', tree).split(b'\0')):
        attributes, name = row.split(b'\t', 1)
        fields = attributes.decode().split()
        if fields[1] != 'blob':
            continue
        path = name.decode('utf-8', 'surrogateescape')
        errors.extend(record_errors(path, git('cat-file', 'blob', fields[2])))
    return errors


def tree_errors(tree):
    """Check the staged index when no tree is given, else that tree."""
    return committed_tree_errors(tree) if tree else index_errors()


def touched_blobs(commit):
    """Names of blobs added/changed/copied/modified by one commit."""
    names = git('diff-tree', '--root', '-m', '--no-commit-id', '--name-only',
                '--diff-filter=ACMT', '--no-renames', '-r', '-z', commit).split(b'\0')
    return [name.decode('utf-8', 'surrogateescape') for name in filter(None, names)]


def changed_tree_errors(base, head):
    """Inspect every touched blob, including files subsequently deleted."""
    errors = []
    for commit in git('rev-list', f'{base}..{head}').decode().splitlines():
        for path in touched_blobs(commit):
            content = git('show', f'{commit}:{path}')
            errors.extend(f'{commit[:12]}: {e}' for e in record_errors(path, content))
    return errors


def directory_errors(root):
    """Check a plain directory tree instead of Git state."""
    errors = []
    for item in sorted(Path(root).rglob('*')):
        if item.is_file():
            errors.extend(record_errors(item.relative_to(root).as_posix(), item.read_bytes()))
    return errors


def observed_reads(reads):
    """Normalize namespaced paths from hash-pinned, identified reads."""
    return {re.sub(r'^ns:[0-9]+//', '/', str(r.get('path')))
            for r in reads if isinstance(r, dict)
            and DIGEST.fullmatch(str(r.get('sha256', ''))) and r.get('file_id')}


def record_shape_errors(value):
    """The record type, reference, base and readset must be current."""
    errors = []
    if value.get('record_type') != 'flowstate-project-metadata-v1' \
            or not REFERENCE.fullmatch(str(value.get('metadata_ref', ''))):
        errors.append('invalid external project metadata record/reference')
    if value.get('base_sha') != git('rev-parse', 'HEAD').decode().strip():
        errors.append('external record base differs from current HEAD')
    reads = value.get('dropbox_reads', [])
    if not isinstance(reads, list):
        return errors + ['dropbox_reads must be a list']
    if not REQUIRED_READS <= observed_reads(reads):
        errors.append('current Dropbox entry/rules/decisions/ACTIVE reads and hashes are required')
    if not DIGEST.fullmatch(str(value.get('docs_pack_manifest_sha256', ''))):
        errors.append('current pack manifest digest is required')
    return errors


def external_record_errors(path):
    """Structural validation of the cached external record."""
    root = Path(git('rev-parse', '--show-toplevel').decode().strip()).resolve()
    if path.resolve().is_relative_to(root):
        return ['metadata record/cache must be outside the code checkout']
    value = json.loads(path.read_text())
    if not isinstance(value, dict):
        return ['external record must be a JSON object']
    errors = record_shape_errors(value)
    attribution = value.get('attribution', {})
    if not isinstance(attribution, dict) or not all(
            isinstance(attribution.get(k), str) and attribution[k].strip()
            for k in ATTRIBUTION_KEYS):
        errors.append('observed attribution or explicit unverified values and source are required')
    return errors


def prepare_message(source, record, output):
    """Validate a fetched external cache and bind its reference to the message."""
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
