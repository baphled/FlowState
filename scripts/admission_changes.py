"""Bind GitHub's changed-file list to manifest entries, one row per changed path."""
import hashlib
from scripts.admission_manifest import DELETION
from scripts.admission_metadata import AdmissionError, SHA


def changed_entries(files):
    """Flatten the GitHub file list into (path, status, blob) rows, one per changed path.

    A rename is a deletion of the old path plus an addition of the new one, which is
    what the local reader (rename detection off) reports. A copy leaves its source
    untouched, so it is only an addition of the new path. Anything else is rejected.
    """
    rows = []
    for item in files:
        status, name, old = item.get('status'), item.get('filename'), item.get('previous_filename')
        if status == 'renamed' and old:
            rows += [(old, 'removed', None), (name, 'added', item.get('sha'))]
        elif status in ('added', 'modified', 'copied') and (status == 'copied' or not old):
            rows.append((name, status, item.get('sha')))
        elif status == 'removed' and not old:
            rows.append((name, status, None))
        else:
            raise AdmissionError('unsupported file status in candidate')
    names = [row[0] for row in rows]
    if not all(isinstance(name, str) and name for name in names) or len(set(names)) != len(names):
        raise AdmissionError('candidate changed-path set differs from Dropbox evidence')
    return rows


def verify_entry(github, name, status, blob_sha, props):
    """Accept a deletion only with the exact deletion entry and a blob only with a matching one."""
    if status == 'removed':
        if props != DELETION or props.get('deleted') is not True:
            raise AdmissionError('unattested deletion in candidate')
        return
    if not isinstance(props, dict) or 'deleted' in props:
        raise AdmissionError('invalid candidate blob evidence')
    if not SHA.fullmatch(str(blob_sha)):
        raise AdmissionError('invalid candidate blob evidence')
    actual = github.blob(blob_sha)
    if hashlib.sha256(actual).hexdigest() != props.get('sha256') or len(actual) != props.get('bytes'):
        raise AdmissionError('candidate blob does not match Dropbox manifest')
