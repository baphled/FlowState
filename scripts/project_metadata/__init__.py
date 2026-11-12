"""Neutral Dropbox metadata contract guard.

The package __init__ owns the message and blob-classification policy
(trailer grammar, claim rejection, record-path rules); Git walks and
external-record validation live in scan, receipt schema checks in
receipts and the CLI in __main__.
"""

import json
import os
import re
from pathlib import Path, PurePosixPath

REFERENCE = re.compile(
    r'dropbox:/FlowState/Metadata/attempts/'
    r'[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*\.json')
TRAILER = re.compile(r'^Project-Metadata: (.+)$', re.M)
CLAIM = re.compile(
    r'^\s*(?:AI-Generated-By|Reviewed-By|AI-Agent|AI-Model|Co-authored-by)\s*:',
    re.I | re.M)
RECORD_PATH = re.compile(
    r'(^|/)(?:receipts?|qualifications?|evidence|reports?|agent-metadata|'
    r'project-metadata|tasks?|plans?|handoffs?|progress)(?:/|[._-])|'
    r'(?:^|/)[^/]*(?:qualification|receipt|attestation|validation-evidence|'
    r'review-result)[^/]*\.(?:md|json|ya?ml|txt|log|zip|db|sqlite)$', re.I)
LEGACY_RECORD = re.compile(r'(^|/)(?:HANDOFF|PROGRESS|PLAN)\.md$', re.I)
GHERKIN = re.compile(rb'github\.com/(?:cucumber/|DATA-DOG/godog)', re.I)
WORKFLOW_BDD = re.compile(rb'(?:\bgodog\b|\bcucumber-js\b|make[ \t]+bdd(?:[-\s;]|$))', re.I)
VERSION_RECORD = re.compile(r'(?:acceptance-receipt|work-packet|qualification|project-metadata)')
CODE_SUFFIXES = {'.go', '.py', '.sh', '.js', '.ts', '.tsx', '.vue'}
TEXT_SUFFIXES = {'.md', '.txt', '.log'}


def referenced_record_errors(reference):
    """When a Dropbox mount is configured, the trailer target must resolve.

    Offline structural validation stays authoritative when no mount is
    configured: this readability check only adds a signal where the local
    ``/FlowState`` (or FLOWSTATE_DROPBOX_ROOT) tree exists.
    """
    root = os.environ.get('FLOWSTATE_DROPBOX_ROOT', '/FlowState')
    target = PurePosixPath(reference).relative_to('dropbox:/') \
        if reference.startswith('dropbox:/') else None
    if target is None or not os.path.isdir(root):
        return []
    path = Path(root) / target
    if not path.is_file():
        return ['trailer reference is missing on the Dropbox mount: ' + reference]
    try:
        json.loads(path.read_text())
    except (ValueError, UnicodeError, OSError):
        return ['trailer reference is not readable JSON: ' + reference]
    return []


def message_errors(message):
    """Validate one neutral, valid Project-Metadata trailer per message."""
    errors = []
    if CLAIM.search(message):
        errors.append('identity/review trailers belong in Dropbox, not commit messages')
    references = TRAILER.findall(message)
    if len(references) >= 2:
        # Merge commits can inherit a trailer from each parent; the
        # generic single-reference error would mislead in that case.
        errors.append('merge commit carries multiple Project-Metadata trailers')
    elif not references or not REFERENCE.fullmatch(references[0]):
        errors.append('one valid Project-Metadata: '
                      'dropbox:/FlowState/Metadata/attempts/'
                      '<attempt>/<record>.json is required')
    elif references:
        errors.extend(referenced_record_errors(references[0]))
    return errors


def toolchain_errors(path, item, content):
    """Forbidden test toolchains are rejected regardless of file class."""
    if item.suffix == '.feature':
        return ['Cucumber/Gherkin feature files are forbidden: ' + path]
    if item.name in ('go.mod', 'go.sum') or item.suffix == '.go':
        if GHERKIN.search(content):
            return ['Cucumber/Godog dependencies and imports are forbidden: ' + path]
    if '.github/workflows/' in path and item.suffix in ('.yml', '.yaml'):
        if WORKFLOW_BDD.search(content):
            return ['Cucumber/Godog workflow execution is forbidden: ' + path]
    return []


def json_value(content):
    """Parsed JSON content, or None for non-JSON or invalid text."""
    try:
        return json.loads(content)
    except (ValueError, UnicodeError):
        return None


def exemption_errors(path, item, value):
    """Code, synthetic fixtures and schema templates are not run records."""
    if item.suffix in CODE_SUFFIXES or item.name.endswith('.schema.json'):
        return []
    if {'testdata', 'fixtures'} & set(item.parts) and isinstance(value, dict) \
            and value.get('synthetic_fixture') is True:
        return []
    return None


def text_errors(path, item, content):
    """Executed-delivery references must not hide in ordinary text files."""
    if item.suffix.lower() not in TEXT_SUFFIXES:
        return []
    text = content.decode('utf-8', 'replace')
    references = re.findall(r'^\s*Project-Metadata:\s*(\S+)\s*$', text, re.M)
    if any(REFERENCE.fullmatch(reference) for reference in references):
        return ['executed delivery record must be stored in Dropbox: ' + path]
    return []


def content_errors(path, value):
    """Structured project-metadata or evidence receipts stay in Dropbox."""
    if not isinstance(value, dict):
        return []
    version = str(value.get('schema_version', value.get('record_type', '')))
    if VERSION_RECORD.search(version):
        return ['project metadata content must be stored in Dropbox: ' + path]
    if {'gate_outcome', 'repo_state', 'evidence'} <= value.keys():
        return ['executed evidence receipt must be stored in Dropbox: ' + path]
    return []


def record_errors(path, content):
    """Full policy for one blob: toolchain, path-name and content checks."""
    item = PurePosixPath(path)
    errors = toolchain_errors(path, item, content)
    if errors:
        return errors
    value = json_value(content) if item.suffix == '.json' else None
    exempt = exemption_errors(path, item, value)
    if exempt is not None:
        return exempt
    if RECORD_PATH.search(path) or LEGACY_RECORD.search(path):
        return ['project metadata file must be stored in Dropbox: ' + path]
    return text_errors(path, item, content) + content_errors(path, value)
