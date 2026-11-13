"""Network client requires a token and rejects incomplete review retrieval."""
import unittest
from unittest.mock import patch
from scripts.review_handoff_io import HandoffError
from scripts.review_handoff_net import github_get


class GitHubReviewNetworkTests(unittest.TestCase):
    def test_missing_token_fails_before_network(self):
        with self.assertRaisesRegex(HandoffError, 'GITHUB_TOKEN'):
            github_get('/repos/example/repo/pulls/1', None)

    def test_http_error_blocks_handoff(self):
        from urllib.error import URLError
        with patch('scripts.review_handoff_net.urlopen', side_effect=URLError('offline')):
            with self.assertRaisesRegex(HandoffError, 'retrieval failed'):
                github_get('/repos/example/repo/pulls/1', 'synthetic-token')


if __name__ == '__main__':
    unittest.main()
