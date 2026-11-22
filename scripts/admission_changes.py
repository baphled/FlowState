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


REGULAR_MODES = ('100644', '100755')


def mode_paths(files):
    """Paths whose mode must be read: every non-removed path at the commit, and at the parent
    only those GitHub reports as other than plainly added (they may replace a parent entry)."""
    live = [(item.get('status'), item.get('filename')) for item in files if item.get('status') != 'removed']
    return ([name for _, name in live if isinstance(name, str)],
            [name for status, name in live if status != 'added' and isinstance(name, str)])


def check_modes(files, current, previous):
    """Fail closed unless every non-removed path is a regular blob in the commit's tree.

    `current` and `previous` map path to (mode, type, blob sha) at the commit and its parent.
    The tree's blob sha must equal the changed-file row's sha, and a path must exist in the commit
    tree. Like the local reader, a replaced parent entry that was not a regular file is a type
    change and is rejected; a deletion is never examined, so removing a symlink stays allowed.
    """
    for item in files:
        name = item.get('filename')
        if item.get('status') == 'removed' or not isinstance(name, str):
            continue
        entry = current.get(name)
        if entry is None:
            raise AdmissionError('candidate path missing from commit tree')
        if entry[0] not in REGULAR_MODES or entry[1] != 'blob':
            raise AdmissionError('candidate files must be regular blobs')
        if entry[2] != item.get('sha'):
            raise AdmissionError('candidate tree blob differs from changed-file evidence')
        before = previous.get(name)
        if item.get('status') != 'added' and before is not None and before[0] not in REGULAR_MODES:
            raise AdmissionError('unsupported file status in candidate')


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
