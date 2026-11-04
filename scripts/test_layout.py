"""Enforce one named test companion per Go implementation in changed sources.

This is a committed-diff gate. It intentionally does not make unrelated
legacy test files fail a PR that never touched them.
"""
from pathlib import PurePosixPath


def test_layout_errors(present_files, changed_files):
    """Reject changed non-suite test files with no exact implementation owner."""
    present = set(present_files)
    findings = []
    for path in sorted(set(changed_files)):
        if not path.endswith("_test.go"):
            continue
        if PurePosixPath(path).name == "suite_test.go":
            continue  # Ginkgo bootstrap, not a place for behavioural specs.
        owner = path[:-len("_test.go")] + ".go"
        if owner not in present:
            findings.append(
                f"{path}: missing paired implementation {owner}; "
                "place Ginkgo specs in the owning <implementation>_test.go"
            )
    return findings
