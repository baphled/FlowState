"""PR inputs are data, never inline bash or JavaScript program source."""

from pathlib import Path
import unittest

SOURCE = (Path(__file__).resolve().parents[2] /
          ".github/workflows/pr-validation.yml").read_text()


class PRValidationSecurity(unittest.TestCase):
    def test_pr_title_cannot_be_interpolated_into_bash_source(self):
        block = SOURCE.split("  validate-pr-title:", 1)[1].split(
            "  check-ai-attribution:", 1)[0]
        self.assertIn("PR_TITLE: ${{ github.event.pull_request.title }}", block)
        self.assertIn('run: printf \'%s\\n\' "$PR_TITLE" | npx commitlint', block)
        self.assertNotIn('run: echo "${{ github.event.pull_request.title }}"', block)

    def test_breaking_change_details_cannot_be_interpolated_into_js_source(self):
        block = SOURCE.split("  breaking-changes:", 1)[1].split(
            "  size-label:", 1)[0]
        self.assertIn("BREAKING_COMMITS: ${{ steps.breaking.outputs.commits }}", block)
        self.assertIn("const commits = process.env.BREAKING_COMMITS;", block)
        self.assertNotIn("const commits = `${{", block)


if __name__ == "__main__":
    unittest.main()
