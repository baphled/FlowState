"""Dependency-free structural validation of attempt receipts."""
import json
import re
import sys
from pathlib import Path

from scripts.project_metadata import REFERENCE

DIGEST = re.compile(r'[0-9a-f]{64}')
RECORD_TYPE = 'flowstate-project-metadata-v1'

RECEIPT_SCHEMA = {
    '$schema': 'https://json-schema.org/draft/2020-12/schema',
    'title': 'FlowState attempt record',
    'type': 'object',
    'required': ['record_type', 'metadata_ref', 'dropbox_reads',
                 'docs_pack_manifest_sha256', 'attribution'],
    'properties': {
        'record_type': {'const': RECORD_TYPE},
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


def manual_errors(value):
    """Yield structural failures without any third-party dependency."""
    for key in RECEIPT_SCHEMA['required']:
        if key not in value:
            yield 'missing required key: ' + key
    if value.get('record_type') != RECORD_TYPE:
        yield 'record_type must be ' + RECORD_TYPE
    if not REFERENCE.fullmatch(str(value.get('metadata_ref', ''))):
        yield 'metadata_ref must be a valid Dropbox attempt reference'
    if not DIGEST.fullmatch(str(value.get('docs_pack_manifest_sha256', ''))):
        yield 'docs_pack_manifest_sha256 must be a 64-hex digest'
    attribution = value.get('attribution')
    if not isinstance(attribution, dict) or not all(
            attribution.get(k) for k in ('agent', 'provider', 'model', 'harness', 'source')):
        yield 'attribution requires agent/provider/model/harness/source'
    for read in value.get('dropbox_reads', []):
        if not DIGEST.fullmatch(str(read.get('sha256', ''))):
            yield 'dropbox_read sha256 must be 64-hex: ' + str(read.get('path'))


def jsonschema_errors(schema, value):
    """Yield Draft 2020-12 validator errors when jsonschema is available."""
    import jsonschema
    yield from ('%s: %s' % (list(e.absolute_path)[0] if e.absolute_path else '<root>', e.message)
                for e in jsonschema.Draft202012Validator(schema).iter_errors(value))


def validate_receipt(schema, path):
    """Print and return failure for a receipt violating the schema."""
    value = json.loads(Path(path).read_text())
    try:
        import jsonschema  # noqa: F401
        check = jsonschema_errors
    except ImportError:
        # Dependency-free structural validation.
        check = manual_errors
    errors = list(check(schema, value))
    for error in errors:
        print('FAIL: ' + error, file=sys.stderr)
    if not errors:
        print('PASS: receipt conforms to flowstate-project-metadata-v1 schema')
    return bool(errors)
