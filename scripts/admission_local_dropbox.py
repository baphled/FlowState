"""Read-only Dropbox evidence adapter against the local, owner-private sync tree."""
import os
import stat

from scripts.admission_io import REFERENCE
from scripts.admission_metadata import AdmissionError
from scripts.admission_transport import strict_json


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
