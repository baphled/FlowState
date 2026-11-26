"""Read-only Dropbox evidence adapter against the local, owner-private sync tree."""
import hashlib
import os
import re
import stat

from scripts.admission_io import REFERENCE
from scripts.admission_metadata import AdmissionError, REQUIRED_READS
from scripts.admission_transport import strict_json

READ_PREFIXES = ("FlowState/Dev/", "FlowState/Knowledge/Plans/", "FlowState/Metadata/")
GOVERNANCE_READS = sorted(REQUIRED_READS - {"/FlowState/Metadata/ACTIVE.json"})
PACK_FIELD = "/FlowState/Metadata/ACTIVE.json (docs_pack_manifest_sha256)"
REMEDY = ("rebuild the external record; no commit changes and no re-review is needed "
          "because the exact-head review binds the record's path")
READ_BOUND = 1048576
DIRECTORY = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
REGULAR = os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC


def _components(path):
    """Split a permitted sync-tree path; refuse any other prefix, empty or dot component."""
    relative = path.removeprefix("/")
    parts = relative.split("/")
    if not relative.startswith(READ_PREFIXES) or {"", ".", ".."} & set(parts):
        raise AdmissionError("Dropbox path is not a readable evidence path: " + path)
    return parts


def _read_under(root, path):
    """Return at most 1 MiB from a regular file, opening every component without symlinks."""
    parts = _components(path)
    opened = []
    try:
        opened.append(os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC))
        for part in parts[:-1]:
            opened.append(os.open(part, DIRECTORY, dir_fd=opened[-1]))
        opened.append(os.open(parts[-1], REGULAR, dir_fd=opened[-1]))
        if not stat.S_ISREG(os.fstat(opened[-1]).st_mode):
            raise AdmissionError("Dropbox file is not a regular file: " + path)
        with os.fdopen(opened[-1], "rb", closefd=False) as handle:
            content = handle.read(READ_BOUND + 1)
    except OSError as error:
        raise AdmissionError("Dropbox file missing, unreadable or behind a symlink: " + path) from error
    finally:
        for descriptor in opened:
            os.close(descriptor)
    if len(content) > READ_BOUND:
        raise AdmissionError("Dropbox file exceeds the 1 MiB bound: " + path)
    return content


def sha256_under(root, relative_path):
    """Hex sha256 of the live bytes at a permitted path under the sync root: the one hashing helper."""
    return hashlib.sha256(_read_under(root, relative_path)).hexdigest()


def verify_read_evidence(record, live_sha256, declared_pack):
    """Refuse a record whose governance read hashes or pack digest differ from live Dropbox bytes.

    live_sha256(path) returns the live digest or raises AdmissionError; declared_pack is the
    docs_pack_manifest_sha256 that ACTIVE.json declares now. ACTIVE.json's own hash is never
    compared: it is a moving pointer. Every refusal names the paths and the remedy.
    """
    items = [i for i in record.get("dropbox_reads") or [] if isinstance(i, dict)]
    observed = [(re.sub(r"^ns:[0-9]+//", "/", str(i.get("path", ""))), i.get("sha256")) for i in items]
    stale = [path for path in GOVERNANCE_READS
             if {h for p, h in observed if p == path} != {live_sha256(path)}]
    if record.get("docs_pack_manifest_sha256") != declared_pack:
        stale.append(PACK_FIELD)
    if stale:
        raise AdmissionError("Dropbox read evidence differs from the live files: "
                             + ", ".join(stale) + "; " + REMEDY)


class LocalDropbox:
    """Read-only adapter mirroring the HTTP client's record() contract locally."""
    def __init__(self, root):
        if not root:
            raise AdmissionError("local Dropbox sync root not configured")
        self.root = os.path.abspath(root)
        self._require_private_root()
    def _require_private_root(self):
        mode = os.stat(self.root)
        private = stat.S_ISDIR(mode.st_mode) and not mode.st_mode & 0o077 \
            and mode.st_uid == os.getuid()
        if not private:
            raise AdmissionError("local Dropbox sync root must be an owner-only directory")
    def read_bytes(self, path):
        """Bytes of a file under /FlowState/Dev/, /FlowState/Knowledge/Plans/ or /FlowState/Metadata/.

        Every component is opened without following symlinks and the file is bounded at 1 MiB;
        any other path, a missing or irregular file and an oversize file raise AdmissionError.
        """
        return _read_under(self.root, path)
    def record(self, reference):
        if not REFERENCE.fullmatch(reference):
            raise AdmissionError("invalid Dropbox record path")
        path = os.path.abspath(os.path.join(
            self.root, reference.removeprefix("dropbox:").lstrip("/")))
        if not path.startswith(os.path.join(self.root, "FlowState") + os.sep):
            raise AdmissionError("Dropbox reference escapes the metadata root")
        try:
            with open(path, "rb") as handle:
                content = handle.read(131073)
        except OSError as error:
            raise AdmissionError("Dropbox record missing or unreadable") from error
        if len(content) > 131072:
            raise AdmissionError("Dropbox record exceeds 128KiB bound")
        try:
            return strict_json(content)
        except ValueError as error:
            raise AdmissionError("Dropbox record invalid JSON") from error
