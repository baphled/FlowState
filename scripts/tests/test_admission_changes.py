"""Exercise the changed-file binding of attested deletions and renames over GitHub-shaped rows."""
import hashlib
import unittest

from scripts.admission_candidate import verify_files
from scripts.admission_changes import check_modes
from scripts.admission_metadata import AdmissionError
from scripts.tests.test_admission_io import HEAD

def git_blob(data):
    return hashlib.sha1(b"blob %d\0" % len(data) + data).hexdigest()
def blob_entry(data):
    return {"sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data)}
DELETED = {"deleted": True}
def github_files(parent, child, renames=(), copies=()):
    """Describe a commit as GitHub reports it; renames and copies map new path to old path."""
    renames, copies = dict(renames), dict(copies)
    files = []
    for name, data in child.items():
        row = {"filename": name, "sha": git_blob(data)}
        if name in renames:
            files.append(dict(row, status="renamed", previous_filename=renames[name]))
        elif name in copies:
            files.append(dict(row, status="copied", previous_filename=copies[name]))
        elif name not in parent or parent[name] != data:
            files.append(dict(row, status="added" if name not in parent else "modified"))
    files += [{"filename": name, "status": "removed"} for name in parent
              if name not in child and name not in renames.values()]
    return files
def attested(parent, child, overrides):
    """The honest manifest (a rename is a deletion plus an addition) with per-path overrides."""
    result = {name: DELETED for name in parent if name not in child}
    result.update({name: blob_entry(data) for name, data in child.items()
                   if parent.get(name) != data})
    result.update(overrides)
    return {name: entry for name, entry in result.items() if entry is not None}
OLD, NEW = b"one\ntwo\n", b"one\nthree\n"
SCENARIOS = {
    "mixed": ({"gone": b"g\n", "keep": b"k\n"}, {"keep": b"k2\n", "fresh": b"f\n"}, {}, {}),
    "all-deleted": ({"gone": b"g\n", "also": b"a\n"}, {}, {}, {}),
    "pure-rename": ({"old": OLD}, {"new": OLD}, {"new": "old"}, {}),
    "rename-with-edits": ({"old": OLD}, {"new": NEW}, {"new": "old"}, {}),
    "rename-onto-existing": ({"old": OLD, "target": b"t\n"}, {"target": OLD}, {}, {}),
    "rename-onto-existing-as-rename": ({"old": OLD, "target": b"t\n"}, {"target": OLD}, {"target": "old"}, {}),
    "copy": ({"src": OLD}, {"src": OLD, "copy": OLD}, {}, {"copy": "src"}),
}
BAD_DELETIONS = [{"deleted": False}, {"deleted": "true"}, {"deleted": 1}, {},
                 {"deleted": True, "sha256": "a" * 64}, {"deleted": True, "bytes": 0}]
def manifest_cases():
    """(scenario, label, overrides, error): error None means the manifest must be accepted."""
    cases = [(name, "attested", {}, None) for name in SCENARIOS]
    cases += [("mixed", "omitted deletion", {"gone": None}, "changed-path"),
              ("mixed", "phantom deletion", {"ghost": DELETED}, "changed-path"),
              ("mixed", "deletion of modified file", {"keep": DELETED}, "invalid candidate blob"),
              ("mixed", "deletion of added file", {"fresh": DELETED}, "invalid candidate blob"),
              ("mixed", "blob for deleted file", {"gone": blob_entry(b"g\n")}, "unattested deletion"),
              ("mixed", "deleted flag on blob", {"keep": dict(blob_entry(b"k2\n"), deleted=True)},
               "invalid candidate blob"),
              ("mixed", "wrong blob", {"keep": blob_entry(b"zzz")}, "does not match"),
              ("all-deleted", "omitted deletion", {"also": None}, "changed-path"),
              ("pure-rename", "new path only", {"old": None}, "changed-path"),
              ("pure-rename", "old path only", {"new": None}, "changed-path"),
              ("pure-rename", "old path as blob", {"old": blob_entry(OLD)}, "unattested deletion"),
              ("rename-with-edits", "stale blob", {"new": blob_entry(OLD)}, "does not match"),
              ("copy", "source listed as deleted", {"src": DELETED}, "changed-path")]
    cases += [("mixed", "malformed %r" % bad, {"gone": bad}, "unattested deletion") for bad in BAD_DELETIONS]
    return cases
# (label, parent mode or None, commit mode, error): one path `node`; both readers must agree.
MODE_CASES = [("added regular", None, "100644", None), ("added executable", None, "100755", None),
              ("added symlink", None, "120000", "candidate files must be regular blobs"),
              ("added submodule", None, "160000", "candidate files must be regular blobs"),
              ("mode flip", "100644", "100755", None),
              ("file into symlink", "100644", "120000", "candidate files must be regular blobs"),
              ("file into submodule", "100644", "160000", "candidate files must be regular blobs"),
              ("symlink into file", "120000", "100644", "unsupported file status"),
              ("submodule into file", "160000", "100644", "unsupported file status")]
def entry_for(mode, data):
    return (mode, "commit" if mode == "160000" else "blob", git_blob(data))
class ScenarioGitHub:
    def __init__(self, files, blobs):
        self.files, self.blobs = files, blobs
    def commit_files(self, sha):
        return self.files
    def blob(self, sha):
        return self.blobs[sha]

class GitHubDeletions(unittest.TestCase):
    def verify(self, scenario, overrides):
        parent, child, renames, copies = SCENARIOS[scenario]
        github = ScenarioGitHub(github_files(parent, child, renames, copies),
                                {git_blob(data): data for data in child.values()})
        verify_files(github, HEAD, attested(parent, child, overrides))
    def test_shared_manifest_table(self):
        for scenario, label, overrides, error in manifest_cases():
            with self.subTest(scenario=scenario, label=label):
                if error is None:
                    self.verify(scenario, overrides)
                else:
                    with self.assertRaisesRegex(AdmissionError, error):
                        self.verify(scenario, overrides)
    def test_github_rename_needs_previous_filename(self):
        github = ScenarioGitHub([{"filename": "new", "status": "renamed", "sha": git_blob(OLD)}], {})
        with self.assertRaisesRegex(AdmissionError, "unsupported file status"):
            verify_files(github, HEAD, {"new": blob_entry(OLD)})
    def test_unknown_status_and_removed_with_previous_name_fail(self):
        for row in ({"filename": "a", "status": "changed", "sha": git_blob(OLD)},
                    {"filename": "a", "status": "removed", "previous_filename": "b"}):
            with self.subTest(row=row):
                with self.assertRaisesRegex(AdmissionError, "unsupported file status"):
                    verify_files(ScenarioGitHub([row], {}), HEAD, {"a": DELETED})
    def test_rename_target_colliding_with_deleted_path_fails(self):
        files = [{"filename": "b", "previous_filename": "a", "status": "renamed", "sha": git_blob(OLD)},
                 {"filename": "a", "status": "removed"}]
        with self.assertRaisesRegex(AdmissionError, "changed-path"):
            verify_files(ScenarioGitHub(files, {git_blob(OLD): OLD}), HEAD,
                         {"a": DELETED, "b": blob_entry(OLD)})

class PublisherModes(unittest.TestCase):
    def test_shared_mode_table(self):
        for label, before, now, error in MODE_CASES:
            with self.subTest(label=label):
                status = "added" if before is None else "modified"
                files = [{"filename": "node", "sha": git_blob(NEW), "status": status}]
                previous = {} if before is None else {"node": entry_for(before, OLD)}
                if error is None:
                    check_modes(files, {"node": entry_for(now, NEW)}, previous)
                else:
                    with self.assertRaisesRegex(AdmissionError, error):
                        check_modes(files, {"node": entry_for(now, NEW)}, previous)
    def test_removed_symlink_is_never_examined(self):
        check_modes([{"filename": "node", "status": "removed"}], {}, {"node": entry_for("120000", OLD)})

if __name__ == "__main__":
    unittest.main()
