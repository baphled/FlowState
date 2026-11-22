"""Shape of one evidence-manifest entry; kept free of admission imports so records can use it."""
import re

DELETION = {'deleted': True}
DIGEST = re.compile(r'[0-9a-f]{64}')


def manifest_entry_valid(path, entry):
    """A path is attested by exactly {"sha256": <hex>, "bytes": <n>} or exactly {"deleted": true}."""
    if not isinstance(path, str) or not isinstance(entry, dict):
        return False
    if 'deleted' in entry:
        return entry == DELETION and entry['deleted'] is True
    size = entry.get('bytes')
    digest = entry.get('sha256')
    return (set(entry) == {'sha256', 'bytes'} and isinstance(digest, str) and DIGEST.fullmatch(digest) is not None
            and isinstance(size, int) and not isinstance(size, bool) and size >= 0)
