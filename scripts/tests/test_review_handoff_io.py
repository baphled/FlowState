"""Review evidence network and ledger contracts with synthetic test data."""
import unittest
from scripts.review_handoff_io import (
    HandoffError, collect_actionable, evidence_links, github_get, parse_ledger,
)


class ReviewHandoffIOTests(unittest.TestCase):
    def test_no_token_blocks_network(self):
        with self.assertRaisesRegex(HandoffError, 'GITHUB_TOKEN'):
            github_get('/repos/example/repo/pulls/1', None)

    def test_explicit_ids_only(self):
        self.assertEqual(evidence_links({'body': 'F260-01 fix; other words'}),
                         {'F260-01'})

    def test_inline_paths_preserved(self):
        x = collect_actionable([], [], [{'body': 'F260-01', 'path': 'app.go', 'line': 25}])
        self.assertEqual(x['F260-01'][0]['path'], 'app.go')
        self.assertEqual(x['F260-01'][0]['line'], 25)

    def test_malformed_github_data_fails_closed(self):
        with self.assertRaisesRegex(HandoffError, 'Malformed'):
            collect_actionable([], [], [None])

    def test_missing_ledger_findings_fails_closed(self):
        with self.assertRaisesRegex(HandoffError, 'must contain'):
            parse_ledger({'pr': 260, 'head_sha': 'a' * 40}, 260, 'a' * 40)


if __name__ == '__main__':
    unittest.main()
