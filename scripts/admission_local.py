"""Read committed local Git objects and external evidence without repository writes."""
import subprocess
from pathlib import Path

from scripts.admission_metadata import AdmissionError, SHA
from scripts.admission_transport import strict_json


def git(*args):
    """Return Git bytes; callers pass validated object IDs rather than shell text."""
    return subprocess.check_output(['git', *args], stderr=subprocess.PIPE)


def local_commits(base, head):
    """Read a nonempty bounded candidate range; the shared validator checks linearity."""
    if not all(SHA.fullmatch(value) for value in (base, head)):
        raise AdmissionError('base and head must be exact 40-character commit SHAs')
    git('merge-base', '--is-ancestor', base, head)
    shas = git('rev-list', '--reverse', '--topo-order', f'{base}..{head}').decode().split()
    if not shas or len(shas) > 1000:
        raise AdmissionError('empty candidate or more than 1000 commits')
    return [local_commit(sha) for sha in shas]


def local_commit(sha):
    """Read parent IDs and complete message from the immutable commit object."""
    parents = git('show', '-s', '--format=%P', sha).decode().split()
    message = git('show', '-s', '--format=%B', sha).decode('utf-8')
    return {'sha': sha, 'parents': [{'sha': parent} for parent in parents],
            'commit': {'message': message}}


class LocalGit:
    """Implement the admission manifest reader over local immutable Git blobs."""
    def commit_files(self, sha):
        rows = git('diff-tree', '--no-commit-id', '-r', '--raw', '-z',
                   '--no-renames', sha).split(b'\0')
        files = []
        for attrs, name in zip(rows[0::2], rows[1::2]):
            _, mode, _, blob, status = attrs.split()
            if mode not in (b'100644', b'100755'):
                raise AdmissionError('candidate files must be regular blobs')
            files.append({'filename': name.decode('utf-8'), 'sha': blob.decode(),
                          'status': {b'A': 'added', b'M': 'modified'}.get(status, 'unsupported')})
        return files

    def blob(self, sha):
        return git('cat-file', 'blob', sha)


class CachedRecords:
    """Read a caller-fetched Dropbox record map outside checkout; never authenticate it."""
    def __init__(self, filename):
        path = Path(filename).resolve(strict=True)
        root = Path(git('rev-parse', '--show-toplevel').decode().strip()).resolve()
        if path.is_relative_to(root):
            raise AdmissionError('evidence cache must be outside the checkout')
        with path.open('rb') as stream:
            content = stream.read(8388609)
        if len(content) > 8388608:
            raise AdmissionError('evidence cache exceeds 8MiB bound')
        self.records = strict_json(content)
        if not isinstance(self.records, dict):
            raise AdmissionError('evidence cache must map Dropbox references to records')

    def record(self, reference):
        if reference not in self.records:
            raise AdmissionError('Dropbox record missing from external cache: ' + reference)
        return self.records[reference]
