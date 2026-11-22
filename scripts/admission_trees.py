"""Read git modes from GitHub trees so the publisher agrees with the local reader on file modes."""
from scripts.admission_changes import check_modes, mode_paths
from scripts.admission_metadata import AdmissionError, SHA


class TreeModes:
    """Prove changed paths are regular blobs at an exact commit; `get` is a read-only GitHub GET."""
    def __init__(self, get):
        self.get = lambda path: get("GET", path)
        self.commit_trees, self.listing = {}, (None, {})
    def checked(self, sha, files, record):
        now, before = mode_paths(files)
        if not now:
            return files
        record = record if isinstance(record, dict) else {}
        held = record.get("commit") if isinstance(record.get("commit"), dict) else {}
        held = held.get("tree") if isinstance(held.get("tree"), dict) else {}
        tree, parents = held.get("sha"), record.get("parents")
        if not SHA.fullmatch(str(tree)) or not isinstance(parents, list) or len(parents) != 1:
            raise AdmissionError("GitHub commit tree evidence unavailable")
        self.commit_trees[sha] = tree
        current = self.tree_modes(tree, now)
        previous = {}
        if before:
            previous = self.tree_modes(self.parent_tree(parents[0]), before)
        check_modes(files, current, previous)
        return files
    def parent_tree(self, link):
        """The tree sha of the parent commit named by a commit record's parent link."""
        parent = str(link.get("sha") if isinstance(link, dict) else None)
        if parent not in self.commit_trees:
            record = self.get("/git/commits/" + parent) if SHA.fullmatch(parent) else None
            held = record.get("tree") if isinstance(record, dict) else None
            ptree = held.get("sha") if isinstance(held, dict) else None
            if not SHA.fullmatch(str(ptree)) or record.get("sha") != parent:
                raise AdmissionError("GitHub parent tree evidence unavailable")
            self.commit_trees[parent] = ptree
        return self.commit_trees[parent]
    def tree_modes(self, tree, paths):
        """Map each requested path to (mode, type, sha) in `tree`; absent paths are omitted.

        One recursive read serves every path. If GitHub reports it truncated, only the directories
        on the changed paths are walked, one level per request; a truncated level is rejected.
        """
        if self.listing[0] == tree:
            return {name: self.listing[1][name] for name in paths if name in self.listing[1]}
        record = self.get(f"/git/trees/{tree}?recursive=1")
        entries = self.tree_level_entries(record, tree, "path")
        if record.get("truncated") is True:
            return {name: found for name in paths if (found := self.walk(tree, name))}
        self.listing = (tree, entries)
        return {name: entries[name] for name in paths if name in entries}
    def tree_level_entries(self, record, tree, key):
        rows = record.get("tree") if isinstance(record, dict) else None
        if not isinstance(rows, list) or record.get("sha") != tree or not isinstance(record.get("truncated"), bool):
            raise AdmissionError("invalid GitHub tree response")
        entries = {}
        for row in rows:
            name = row.get(key) if isinstance(row, dict) else None
            if not isinstance(name, str) or name in entries:
                raise AdmissionError("invalid GitHub tree response")
            entries[name] = (row.get("mode"), row.get("type"), row.get("sha"))
        return entries
    def walk(self, tree, name):
        """Find one path by reading each directory on it, a single non-recursive tree per level."""
        parts = name.split("/")
        for depth, part in enumerate(parts):
            record = self.get("/git/trees/" + tree)
            level = self.tree_level_entries(record, tree, "path")
            if record["truncated"]:
                raise AdmissionError("GitHub tree too large to read modes")
            found = level.get(part)
            if found is None or depth == len(parts) - 1:
                return found
            if found[1] != "tree" or not SHA.fullmatch(str(found[2])):
                return None
            tree = found[2]
