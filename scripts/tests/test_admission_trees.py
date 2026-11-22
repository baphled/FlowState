"""Publisher mode reads over modelled GitHub commit, git-commit and git-tree responses."""
import unittest

from scripts.admission_io import GitHub
from scripts.admission_metadata import AdmissionError
from scripts.admission_trees import TreeModes
from scripts.tests.test_admission_io import HEAD, PARENT

TREE, PTREE, BLOB, OTHER, DIR = "a" * 40, "b" * 40, "c" * 40, "d" * 40, "e" * 40
def tree_row(path, mode="100644", sha=BLOB, kind="blob"):
    return {"path": path, "mode": mode, "type": kind, "sha": sha}
class ModeServer:
    """Models GitHub's commit, git-commit and git-tree responses; records every request path."""
    def __init__(self, files, now, before=(), truncated=False, wrong_tree=False):
        self.files, self.calls, self.truncated = files, [], truncated
        self.trees = {TREE: list(now), PTREE: list(before)}
        self.wrong_tree = wrong_tree
    def level(self, tree, rows):
        """One directory of a root tree; the single directory `d` is the tree DIR."""
        if tree == DIR:
            return [dict(r, path=r["path"][2:]) for r in self.trees[TREE] if r["path"].startswith("d/")]
        top = [r for r in rows if "/" not in r["path"]]
        return top + ([tree_row("d", "040000", DIR, "tree")] if any("/" in r["path"] for r in rows) else [])
    def request(self, method, path):
        self.calls.append(path)
        if path.startswith("/commits/"):
            sha = path.split("/")[2].split("?")[0]
            tree = OTHER if self.wrong_tree and sha == HEAD else TREE
            return {"sha": sha, "files": self.files, "parents": [{"sha": PARENT}],
                    "commit": {"tree": {"sha": tree}}}
        if path == "/git/commits/" + PARENT:
            return {"sha": PARENT, "tree": {"sha": PTREE}}
        tree = path.split("/")[3].split("?")[0]
        rows = self.trees.get(tree, [{"path": "x", "mode": "100644", "type": "blob", "sha": OTHER}])
        if "recursive" not in path:
            rows = self.level(tree, rows)
        return {"sha": tree, "tree": rows, "truncated": self.truncated and "recursive" in path}
def mode_github(server):
    gh = GitHub("baphled/FlowState", "synthetic")
    gh.request = server.request
    return gh
def row(name="link", status="added", sha=BLOB, **extra):
    return dict({"filename": name, "status": status, "sha": sha}, **extra)

class PublisherModes(unittest.TestCase):
    def read(self, files, now, before=(), **options):
        server = ModeServer(files, now, before, **options)
        return mode_github(server).commit_files(HEAD), server
    def test_regular_and_executable_files_are_accepted(self):
        for mode in ("100644", "100755"):
            with self.subTest(mode=mode):
                files, _ = self.read([row()], [tree_row("link", mode)])
                self.assertEqual(files, [row()])
    def test_added_symlink_and_submodule_are_rejected(self):
        for mode, kind in (("120000", "blob"), ("160000", "commit")):
            with self.subTest(mode=mode):
                with self.assertRaisesRegex(AdmissionError, "candidate files must be regular blobs"):
                    self.read([row()], [tree_row("link", mode, kind=kind)])
    def test_modified_into_symlink_is_rejected(self):
        with self.assertRaisesRegex(AdmissionError, "regular blobs"):
            self.read([row(status="modified")], [tree_row("link", "120000")], [tree_row("link")])
    def test_renamed_symlink_is_rejected(self):
        files = [row("new", "renamed", previous_filename="old")]
        with self.assertRaisesRegex(AdmissionError, "regular blobs"):
            self.read(files, [tree_row("new", "120000")], [tree_row("old", "120000")])
    def test_symlink_replaced_by_regular_file_is_a_type_change(self):
        with self.assertRaisesRegex(AdmissionError, "unsupported file status"):
            self.read([row(status="modified")], [tree_row("link")], [tree_row("link", "120000")])
    def test_deleted_symlink_is_accepted_without_a_tree_read(self):
        files, server = self.read([{"filename": "link", "status": "removed"}], [tree_row("link", "120000")])
        self.assertEqual((len(files), len(server.calls)), (1, 1))
    def test_tree_blob_differing_from_files_row_is_rejected(self):
        with self.assertRaisesRegex(AdmissionError, "tree blob differs"):
            self.read([row()], [tree_row("link", sha=OTHER)])
    def test_path_missing_from_tree_is_rejected(self):
        with self.assertRaisesRegex(AdmissionError, "missing from commit tree"):
            self.read([row()], [tree_row("other")])
    def test_mode_is_read_from_the_tree_of_the_exact_commit(self):
        with self.assertRaisesRegex(AdmissionError, "missing from commit tree"):
            self.read([row()], [tree_row("link")], wrong_tree=True)
    def test_truncated_tree_falls_back_to_directory_walk(self):
        files, server = self.read([row("d/link")], [tree_row("d/link")], truncated=True)
        self.assertEqual(files, [row("d/link")])
        self.assertEqual([c for c in server.calls if "git/trees" in c],
                         [f"/git/trees/{TREE}?recursive=1", f"/git/trees/{TREE}", f"/git/trees/{DIR}"])
    def test_truncated_tree_fallback_still_rejects_a_symlink(self):
        with self.assertRaisesRegex(AdmissionError, "regular blobs"):
            self.read([row("link")], [tree_row("link", "120000")], truncated=True)
    def test_truncated_level_is_rejected(self):
        server = ModeServer([row()], [tree_row("link")], truncated=True)
        server.request, original = lambda m, p: dict(original(m, p), truncated=True), server.request
        with self.assertRaisesRegex(AdmissionError, "too large"):
            mode_github(server).commit_files(HEAD)
    def test_an_added_file_costs_one_tree_request(self):
        server = ModeServer([row()], [tree_row("link")])
        gh = mode_github(server)
        gh.commit_files(HEAD)
        self.assertEqual([c for c in server.calls if c.startswith("/git/")], [f"/git/trees/{TREE}?recursive=1"])
    def test_modified_file_reads_parent_commit_once_and_two_trees(self):
        _, server = self.read([row(status="modified")], [tree_row("link")], [tree_row("link")])
        self.assertEqual([c for c in server.calls if c.startswith("/git/")],
                         [f"/git/trees/{TREE}?recursive=1", "/git/commits/" + PARENT,
                          f"/git/trees/{PTREE}?recursive=1"])


GOOD_TREE = {"sha": TREE, "tree": [tree_row("link")], "truncated": False}
COMMIT = {"sha": HEAD, "parents": [{"sha": PARENT}], "commit": {"tree": {"sha": TREE}}}
BAD_TREES = {
    "a list": [], "null": None, "a string": "tree", "no tree": {"sha": TREE, "truncated": False},
    "another tree's sha": dict(GOOD_TREE, sha=OTHER), "no sha": {"tree": [], "truncated": False},
    "truncated as text": dict(GOOD_TREE, truncated="false"), "truncated missing": {"sha": TREE, "tree": []},
    "an entry that is not an object": dict(GOOD_TREE, tree=["link"]),
    "an entry without a path": dict(GOOD_TREE, tree=[{"mode": "100644", "type": "blob", "sha": BLOB}]),
    "a duplicated path": dict(GOOD_TREE, tree=[tree_row("link"), tree_row("link", "120000")]),
}
BAD_COMMITS = {
    "a list": [], "no commit": {"sha": HEAD, "parents": [{"sha": PARENT}]},
    "commit as text": dict(COMMIT, commit="x"), "tree as text": dict(COMMIT, commit={"tree": "x"}),
    "a short tree sha": dict(COMMIT, commit={"tree": {"sha": "abc"}}),
    "no parents": dict(COMMIT, parents=None), "two parents": dict(COMMIT, parents=[{"sha": PARENT}, {"sha": OTHER}]),
}
BAD_PARENTS = {
    "another commit's sha": {"sha": OTHER, "tree": {"sha": PTREE}}, "null": None,
    "tree as text": {"sha": PARENT, "tree": "x"},
    "no tree": {"sha": PARENT}, "a short tree sha": {"sha": PARENT, "tree": {"sha": "abc"}},
}


class FailClosedResponses(unittest.TestCase):
    """Every malformed or mismatched GitHub response is a rejection, never a pass."""
    def modes(self, responses):
        return TreeModes(lambda method, path: responses[path])
    def test_malformed_tree_response_is_rejected(self):
        for name, response in BAD_TREES.items():
            with self.subTest(name), self.assertRaisesRegex(AdmissionError, "invalid GitHub tree response"):
                self.modes({f"/git/trees/{TREE}?recursive=1": response}).checked(HEAD, [row()], COMMIT)
    def test_malformed_commit_record_is_rejected(self):
        for name, record in BAD_COMMITS.items():
            with self.subTest(name), self.assertRaisesRegex(AdmissionError, "commit tree evidence unavailable"):
                self.modes({}).checked(HEAD, [row()], record)
    def test_malformed_parent_commit_is_rejected(self):
        for name, parent in BAD_PARENTS.items():
            responses = {f"/git/trees/{TREE}?recursive=1": GOOD_TREE, "/git/commits/" + PARENT: parent}
            with self.subTest(name), self.assertRaisesRegex(AdmissionError, "parent tree evidence unavailable"):
                self.modes(responses).checked(HEAD, [row(status="modified")], COMMIT)
    def test_parent_given_as_text_is_rejected(self):
        with self.assertRaisesRegex(AdmissionError, "parent tree evidence unavailable"):
            self.modes({f"/git/trees/{TREE}?recursive=1": GOOD_TREE}).checked(
                HEAD, [row(status="modified")], dict(COMMIT, parents=[PARENT]))
    def test_regular_mode_on_an_entry_that_is_not_a_blob_is_rejected(self):
        for kind in ("tree", "commit", None):
            tree = dict(GOOD_TREE, tree=[tree_row("link", kind=kind)])
            with self.subTest(kind), self.assertRaisesRegex(AdmissionError, "regular blobs"):
                self.modes({f"/git/trees/{TREE}?recursive=1": tree}).checked(HEAD, [row()], COMMIT)
    def walked(self, levels):
        """A truncated recursive listing, then the given non-recursive levels."""
        responses = {f"/git/trees/{TREE}?recursive=1": dict(GOOD_TREE, truncated=True)}
        responses.update({"/git/trees/" + sha: level for sha, level in levels.items()})
        return self.modes(responses).checked(HEAD, [row("d/link")], COMMIT)
    def test_walk_through_a_path_component_that_is_a_file_finds_nothing(self):
        root = {"sha": TREE, "tree": [tree_row("d", sha=DIR)], "truncated": False}
        inner = {"sha": DIR, "tree": [tree_row("link")], "truncated": False}
        with self.assertRaisesRegex(AdmissionError, "missing from commit tree"):
            self.walked({TREE: root, DIR: inner})
    def test_walk_rejects_a_level_answering_for_another_tree(self):
        root = {"sha": TREE, "tree": [tree_row("d", "040000", DIR, "tree")], "truncated": False}
        inner = {"sha": OTHER, "tree": [tree_row("link")], "truncated": False}
        with self.assertRaisesRegex(AdmissionError, "invalid GitHub tree response"):
            self.walked({TREE: root, DIR: inner})
    def test_walk_rejects_a_malformed_level(self):
        for name, level in BAD_TREES.items():
            with self.subTest(name), self.assertRaisesRegex(AdmissionError, "invalid GitHub tree response"):
                self.walked({TREE: level})


if __name__ == "__main__":
    unittest.main()
