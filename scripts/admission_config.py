"""Load private runner configuration; never accept configuration from candidate PRs."""
import os
import stat
from pathlib import Path

from scripts.admission_metadata import AdmissionError
from scripts.admission_transport import strict_json


def private_file(path):
    """Require an owner-only regular file; do not disclose file contents in errors."""
    file = Path(path)
    try:
        mode = file.stat()
    except FileNotFoundError as error:
        raise AdmissionError('runner configuration/key file missing: ' + str(file)) from error
    if not stat.S_ISREG(mode.st_mode) or mode.st_mode & 0o077 or mode.st_uid != os.getuid():
        raise AdmissionError('runner configuration and key must be owner-only regular files')
    return file.read_bytes()


def private_directory(path):
    """Require an owner-only directory for the local Dropbox sync tree root."""
    file = Path(path)
    try:
        mode = file.stat()
    except FileNotFoundError as error:
        raise AdmissionError('runner dropbox_local_path directory missing: ' + str(file)) from error
    if not stat.S_ISDIR(mode.st_mode) or mode.st_mode & 0o077 or mode.st_uid != os.getuid():
        raise AdmissionError('runner dropbox_local_path must be an owner-only directory')
    return file


def configuration(path):
    """Validate required credentials/authorities before starting the polling service."""
    from scripts.app_credentials import app_jwt
    from scripts.review_signature import decode_hex
    value = strict_json(private_file(path))
    if not isinstance(value, dict):
        raise AdmissionError('runner configuration must be an object')
    for name in ('github_read_token', 'private_key_file'):
        if not isinstance(value.get(name), str) or not value[name].strip():
            raise AdmissionError('missing runner configuration: ' + name)
    token = value.get('dropbox_read_token')
    local = value.get('dropbox_local_path')
    if bool(isinstance(token, str) and token.strip()) == bool(isinstance(local, str) and local.strip()):
        raise AdmissionError(
            'exactly one of dropbox_read_token or dropbox_local_path is required')
    if local is not None:
        value['dropbox_local_path'] = str(private_directory(local))
    for name in ('app_id', 'installation_id', 'repository_id'):
        if type(value.get(name)) is not int or value[name] <= 0:
            raise AdmissionError('positive runner identifier required: ' + name)
    for name in ('review_public_key', 'execution_public_key'):
        decode_hex(value.get(name), 32, name)
    if value['review_public_key'] == value['execution_public_key']:
        raise AdmissionError('distinct review and execution authorities required')
    if value.get('repository') != 'baphled/FlowState' or value['app_id'] != 5728089:
        raise AdmissionError('runner must use the qualified FlowState repository and App')
    value['private_key'] = private_file(value['private_key_file'])
    app_jwt(value['app_id'], value['private_key'], 0)  # validate RSA material, no network
    return value
