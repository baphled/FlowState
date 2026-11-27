import io
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock
from urllib.error import HTTPError
from scripts.reconcile_changes import ApiSource, Change, GitSource
from scripts.reconcile_config import ReconcileError

A, B = "a" * 40, "b" * 40
TA, TB = "1" * 40, "2" * 40


def http_error(code):
    error = HTTPError("u", code, "x", {}, io.BytesIO(b""))
    error.close()
    return error


def git(*args, cwd=None, stdin=None):
    ident = {"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"}
    return subprocess.run(("git",) + args, cwd=cwd, env=dict(os.environ, **ident), check=True,
                          capture_output=True, text=True, input=stdin).stdout.strip()


def write(root, name, text):
    path = os.path.join(root, name)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as handle:
        handle.write(text)


def relink(root, name, target):
    os.remove(os.path.join(root, name))
    os.symlink(target, os.path.join(root, name))


def snapshot(root, message, gitlink):
    """Stage everything, point the gitlink `mod` at `gitlink` (add -A would drop it) and commit."""
    git("add", "-A", cwd=root)
    git("update-index", "--add", "--cacheinfo", "160000,%s,mod" % gitlink, cwd=root)
    git("commit", "-q", "-m", message, cwd=root)
    return git("rev-parse", "HEAD", cwd=root)


def build(root):
    """c1 -> c2 (rename, symlink retarget, file->symlink, gitlink move under .gitmodules ignore=all)
    -> c3 (200 adds) -> c4 (201 changes); `other` is unrelated."""
    git("init", "-q", root)
    git("config", "commit.gpgsign", "false", cwd=root)
    for name, text in (("a.txt", "a\n"), ("old.txt", "same\n" * 5), ("sub/x.txt", "x\n"), ("kind", "regular\n"),
                       (".gitmodules", '[submodule "mod"]\n\tpath = mod\n\turl = ./x\n\tignore = all\n')):
        write(root, name, text)
    os.symlink("a.txt", os.path.join(root, "ln"))
    c1 = snapshot(root, "first", "1" * 40)
    os.rename(os.path.join(root, "old.txt"), os.path.join(root, "new.txt"))
    relink(root, "ln", "sub/x.txt")
    relink(root, "kind", "a.txt")
    write(root, "a.txt", "changed\n")
    c2 = snapshot(root, "second\n\nbody line", "2" * 40)
    for i in range(200):
        write(root, "bulk/f%03d" % i, "x")
    git("add", "bulk", cwd=root)
    git("commit", "-q", "-m", "bulk", cwd=root)
    c3 = git("rev-parse", "HEAD", cwd=root)
    git("rm", "-rq", "bulk", cwd=root)
    write(root, "extra.txt", "e")
    git("add", "extra.txt", cwd=root)
    git("commit", "-q", "-m", "churn", cwd=root)
    other = git("commit-tree", git("mktree", cwd=root, stdin=""), "-m", "unrelated", cwd=root)
    return c1, c2, c3, git("rev-parse", "HEAD", cwd=root), other


def fork(root, parent, name):
    """A child of `parent` that adds one file, built with plumbing so the checkout and index stay untouched."""
    blob = git("hash-object", "-w", "--stdin", cwd=root, stdin="f")
    rows = git("ls-tree", parent, cwd=root).splitlines() + ["100644 blob %s\t%s" % (blob, name)]
    return git("commit-tree", git("mktree", cwd=root, stdin="\n".join(rows) + "\n"), "-p", parent, "-m", name,
               cwd=root)


class GitSourceTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.root = cls.enterClassContext(tempfile.TemporaryDirectory())
        cls.c1, cls.c2, cls.c3, cls.c4, cls.other = build(cls.root)
        cls.base, cls.head = fork(cls.root, cls.c2, "base.txt"), fork(cls.root, cls.c2, "head.txt")
        cls.source = GitSource(cls.root)

    def test_rename_symlink_gitlink_and_type_changes_are_reported_with_modes(self):
        self.assertEqual(set(self.source.changed_paths(self.c1, self.c2)), {
            Change("old.txt", "D", "000000", "100644"), Change("new.txt", "A", "100644", "000000"),
            Change("ln", "M", "120000", "120000"), Change("a.txt", "M", "100644", "100644"),
            Change("kind", "M", "120000", "100644"), Change("mod", "M", "160000", "160000")})

    def test_symlink_gitlink_and_type_changes_are_special(self):
        changes = self.source.changed_paths(self.c1, self.c2)
        self.assertEqual({c.path for c in changes if c.special}, {"ln", "mod", "kind"})

    def test_two_hundred_paths_pass_and_two_hundred_and_one_raise(self):
        self.assertEqual(len(self.source.changed_paths(self.c2, self.c3)), 200)
        self.assertRaises(ReconcileError, self.source.changed_paths, self.c3, self.c4)

    def test_diverged_base_lists_the_merge_base_diff_as_the_compare_endpoint_does(self):
        """`base` and `head` branch from c2 and each add a file; GitHub's compare reports
        `diverged` and the API source refuses, so the git source refuses too: the sources agree."""
        table = routes([{"filename": "head.txt", "status": "added"}], status="diverged",
                       head_rows=[("head.txt", "100644")], base=self.base, head=self.head)
        for source in (self.source, ApiSource(FakeApi(table))):
            with self.assertRaises(ReconcileError):
                source.changed_paths(self.base, self.head)

    def test_behind_range_is_refused_by_both_sources(self):
        """When `head` is an ancestor of `base`, GitHub reports `behind` and the API source refuses;
        the git source refuses too, so both sources agree."""
        table = routes([], status="behind", base=self.c2, head=self.c1)
        for source in (self.source, ApiSource(FakeApi(table))):
            with self.assertRaises(ReconcileError):
                source.changed_paths(self.c2, self.c1)

    def test_user_git_config_cannot_empty_the_diff(self):
        """A global `diff.relative=true` in the user's HOME would silently empty changed_paths when
        the repository is a subdirectory; the pin to /dev/null prevents it."""
        home = self.enterContext(tempfile.TemporaryDirectory())
        Path(home, ".gitconfig").write_text("[diff]\n\trelative = true\n")
        env = {**os.environ, "HOME": home}
        with mock.patch.dict(os.environ, env):
            self.assertEqual(len(GitSource(os.path.join(self.root, "sub")).changed_paths(self.c1, self.c2)), 6)

    def test_tree_lists_files_and_directories_with_modes(self):
        entries = self.source.tree_entries(self.c2)
        self.assertEqual((entries["new.txt"], entries["ln"], entries["mod"], entries["sub"], "old.txt" in entries),
                         ("100644", "120000", "160000", "040000", False))
        # Pin -r: without recursion the subdirectory entry proves its child falsely absent.
        self.assertEqual(entries["sub/x.txt"], "100644")

    def test_unknown_commit_or_non_commit_object_raises(self):
        tree = git("rev-parse", self.c1 + "^{tree}", cwd=self.root)
        for call, value in ((self.source.tree_entries, "f" * 40), (self.source.commit_message, "f" * 40),
                            (self.source.commit_message, tree)):
            with self.subTest(call=call.__name__, value=value), self.assertRaises(ReconcileError):
                call(value)

    def test_commit_message_is_the_full_message(self):
        self.assertEqual(self.source.commit_message(self.c2), "second\n\nbody line")

    def test_ancestry(self):
        self.assertEqual([self.source.is_ancestor(self.c1, self.c2), self.source.is_ancestor(self.c2, self.c1),
                          self.source.is_ancestor(self.other, self.c2), self.source.is_ancestor(self.c2, self.c2)],
                         [True, False, False, True])
        self.assertRaises(ReconcileError, self.source.is_ancestor, "f" * 40, self.c2)

    def test_inherited_git_variables_are_ignored(self):
        env = {"GIT_DIR": os.path.join(self.root, "nowhere"), "GIT_WORK_TREE": "/"}
        with mock.patch.dict(os.environ, env):
            self.assertIn("a.txt", self.source.tree_entries(self.c1))


def commit_record(sha, tree, message="m"):
    return {"sha": sha, "commit": {"message": message, "tree": {"sha": tree}}}


def tree_record(sha, rows=(), truncated=False):
    return {"sha": sha, "truncated": truncated, "tree": [{"path": p, "mode": m, "type": "blob"} for p, m in rows]}


def tree_path(sha):
    return "/git/trees/%s?recursive=1" % sha


def compare(base, head):
    return "/compare/%s...%s" % (base, head)


def routes(files, status="ahead", base_rows=(), head_rows=(), base=A, head=B):
    """The API's routes; the compare reply carries the whole file list for the range."""
    return {"/commits/" + base: commit_record(base, TA), "/commits/" + head: commit_record(head, TB),
            tree_path(TA): tree_record(TA, base_rows), tree_path(TB): tree_record(TB, head_rows),
            compare(base, head): {"status": status, "files": files}}


class FakeApi:
    """A GET-only reader; a path with no route stands for a 404."""
    def __init__(self, table):
        self.table, self.seen = table, []

    def __call__(self, path):
        self.seen.append(path)
        reply = self.table[path]
        if isinstance(reply, Exception):
            raise reply
        return reply


def added(count, prefix="f"):
    return [{"filename": "%s%d" % (prefix, i), "status": "added"} for i in range(count)]


class ApiChangedPathsTest(unittest.TestCase):
    def read(self, table):
        self.api = FakeApi(table)
        return ApiSource(self.api).changed_paths(A, B)

    def trees_read(self):
        return [p for p in self.api.seen if "/git/trees" in p]

    def test_rename_is_delete_plus_add_with_modes_from_both_trees(self):
        files = [{"filename": "new", "previous_filename": "old", "status": "renamed"},
                 {"filename": "ln", "status": "modified"}]
        got = self.read(routes(files, base_rows=[("old", "100644"), ("ln", "100644")],
                               head_rows=[("new", "100644"), ("ln", "120000")]))
        self.assertEqual(got, [Change("old", "D", "000000", "100644"), Change("new", "A", "100644", "000000"),
                               Change("ln", "M", "120000", "100644")])
        self.assertEqual([c.special for c in got], [False, False, True])

    def test_one_compare_call_passes_150_files_and_201_raise_before_any_tree_is_read(self):
        """`page` paginates commits only, so one reply carries the whole file list: 150 files need one call
        and 201 exceed MAX_PATHS."""
        self.assertEqual(len(self.read(routes(added(150)))), 150)
        self.assertEqual([p for p in self.api.seen if p.startswith("/compare/")], [compare(A, B)])
        with self.assertRaises(ReconcileError):
            self.read(routes(added(201)))
        self.assertEqual(self.trees_read(), [])

    def test_two_hundred_paths_pass_and_two_hundred_and_one_raise_before_any_tree_is_read(self):
        self.assertEqual(len(self.read(routes(added(200)))), 200)
        with self.assertRaises(ReconcileError):
            self.read(routes(added(201)))
        self.assertEqual(self.trees_read(), [])

    def test_renames_count_as_two_paths_each(self):
        renames = [{"filename": "n%d" % i, "previous_filename": "o%d" % i, "status": "renamed"} for i in range(101)]
        self.assertRaises(ReconcileError, self.read, routes(renames))

    def test_status_other_than_ahead_or_identical_raises(self):
        for status in ("diverged", "behind", "bogus", None):
            with self.subTest(status=status), self.assertRaises(ReconcileError):
                self.read(routes(added(1), status=status))

    def test_identical_range_is_empty_and_reads_no_tree(self):
        self.assertEqual(self.read(routes([], status="identical")), [])
        self.assertEqual(self.trees_read(), [])

    def test_a_reply_without_a_file_list_raises_rather_than_reporting_no_changes(self):
        """The endpoint leaves `files` out of its later pages; an absent list is not an empty one."""
        table = routes(added(1))
        del table[compare(A, B)]["files"]
        self.assertRaises(ReconcileError, self.read, table)

    def test_unsupported_rows_raise(self):
        for row in ({"filename": "x", "status": "unchanged"}, {"filename": "", "status": "added"},
                    {"status": "renamed", "filename": "n"}, {"status": "added"}, "x"):
            with self.subTest(row=row), self.assertRaises(ReconcileError):
                self.read(routes([row]))

    def test_every_transport_failure_raises(self):
        for failure in [OSError("down")] + [http_error(code) for code in (403, 404, 429, 500)]:
            for key in (compare(A, B), tree_path(TB)):
                table = routes(added(1))
                table[key] = failure
                with self.subTest(failure=failure, key=key), self.assertRaises(ReconcileError):
                    self.read(table)

    def test_no_connection_is_opened_by_the_module(self):
        with mock.patch("socket.socket.connect", side_effect=AssertionError("network")):
            self.assertEqual(len(self.read(routes(added(2)))), 2)


class ApiTreeTest(unittest.TestCase):
    def read(self, table):
        return ApiSource(FakeApi(table)).tree_entries(A)

    def table(self, listing):
        return {"/commits/" + A: commit_record(A, TA), tree_path(TA): listing}

    def test_listing_gives_modes_and_a_missing_path_is_absent(self):
        entries = self.read(self.table(tree_record(TA, [("a", "100644"), ("d", "040000")])))
        self.assertEqual((entries, "missing" in entries), ({"a": "100644", "d": "040000"}, False))

    def test_truncated_or_unproven_listing_raises(self):
        for truncated in (True, None, "false", 0):
            with self.subTest(truncated=truncated), self.assertRaises(ReconcileError):
                self.read(self.table(tree_record(TA, [("a", "100644")], truncated=truncated)))

    def test_failure_on_the_commit_or_the_tree_raises(self):
        for code in (403, 404, 429, 500):
            for key in ("/commits/" + A, tree_path(TA)):
                table = self.table(tree_record(TA))
                table[key] = http_error(code)
                with self.subTest(code=code, key=key), self.assertRaises(ReconcileError):
                    self.read(table)

    def test_mismatched_or_malformed_replies_raise(self):
        bad_rows = [{"mode": "100644"}, {"path": "a"}, {"path": 1, "mode": "100644"}, "x"]
        replies = [tree_record(B), dict(tree_record(TA), tree="x"), dict(tree_record(TA), tree={})]
        replies += [dict(tree_record(TA), tree=[row]) for row in bad_rows]
        replies.append(tree_record(TA, [("a", "100644"), ("a", "100644")]))
        for reply in replies:
            with self.subTest(reply=reply), self.assertRaises(ReconcileError):
                self.read(self.table(reply))
        for record in (commit_record(B, TA), commit_record(A, "short")):
            with self.subTest(record=record), self.assertRaises(ReconcileError):
                table = dict(self.table(tree_record(TA)), **{"/commits/" + A: record})
                table[tree_path("short")] = tree_record("short")
                self.read(table)


class ApiCommitTest(unittest.TestCase):
    def test_message_comes_from_the_commit_endpoint(self):
        source = ApiSource(FakeApi({"/commits/" + A: commit_record(A, TA, "subject\n\nbody\n")}))
        self.assertEqual(source.commit_message(A), "subject\n\nbody")

    def test_wrong_sha_or_non_text_message_raises(self):
        for record in (commit_record(B, TA), commit_record(A, TA, message=None), [1]):
            with self.subTest(record=record), self.assertRaises(ReconcileError):
                ApiSource(FakeApi({"/commits/" + A: record})).commit_message(A)

    def ancestry(self, status):
        return ApiSource(FakeApi({compare(A, B): {"status": status, "files": []}})).is_ancestor(A, B)

    def test_ahead_and_identical_hold_and_behind_does_not(self):
        self.assertEqual([self.ancestry(s) for s in ("ahead", "identical", "behind")], [True, True, False])

    def test_diverged_or_unknown_status_raises(self):
        for status in ("diverged", "x", None, ["ahead"]):
            with self.subTest(status=status), self.assertRaises(ReconcileError):
                self.ancestry(status)

    def test_transport_failure_raises(self):
        source = ApiSource(FakeApi({compare(A, B): http_error(404)}))
        self.assertRaises(ReconcileError, source.is_ancestor, A, B)


class MalformedInputTest(unittest.TestCase):
    def test_arguments_that_are_not_full_shas_raise_for_both_sources(self):
        for source in (GitSource("."), ApiSource(FakeApi({}))):
            for bad in ("HEAD", "-x", "a" * 39, "A" * 40, "", None):
                for name, args in (("tree_entries", (bad,)), ("commit_message", (bad,)),
                                   ("changed_paths", (bad, A)), ("changed_paths", (A, bad)),
                                   ("is_ancestor", (bad, A)), ("is_ancestor", (A, bad))):
                    with self.subTest(source=type(source).__name__, bad=bad, call=name, args=args), \
                            self.assertRaises(ReconcileError):
                        getattr(source, name)(*args)


if __name__ == "__main__":
    unittest.main()
