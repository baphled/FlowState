"""Prevent dedicated security scanning from silently excluding next."""

from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]


class SecurityWorkflow(unittest.TestCase):
    def test_pr_scan_covers_next_and_release(self):
        content = (ROOT / ".github/workflows/security.yml").read_text()
        self.assertIn("branches: [main, next]", content)

    def test_toolchain_tracks_branch_go_module(self):
        content = (ROOT / ".github/workflows/security.yml").read_text()
        self.assertIn("go-version-file: go.mod", content)
        self.assertNotIn('go-version: "1.25.7"', content)

    def test_workflow_and_test_edits_trigger_security_scans(self):
        content = (ROOT / ".github/workflows/security.yml").read_text()
        self.assertIn('".github/workflows/security.yml"', content)
        self.assertIn('"scripts/tests/test_security_workflow.py"', content)

    def test_ci_workflows_run_pinned_govulncheck(self):
        for name in ("ci.yml", "ci-next.yml"):
            with self.subTest(workflow=name):
                content = (ROOT / f".github/workflows/{name}").read_text()
                self.assertIn('GOVULNCHECK_VERSION: "v1.9.0"', content)
                self.assertIn(
                    "go run golang.org/x/vuln/cmd/govulncheck@${{ env.GOVULNCHECK_VERSION }} ./...",
                    content,
                )


if __name__ == "__main__":
    unittest.main()
