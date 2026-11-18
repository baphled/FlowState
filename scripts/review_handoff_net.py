"""Read paginated GitHub review data with strict transport failures."""
import json
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen
from scripts.review_handoff_io import HandoffError


def github_get(path, token):
    """Read every page of a GitHub REST resource with a required token."""
    if not token:
        raise HandoffError('GITHUB_TOKEN is required for verified PR retrieval')
    results = []
    for page in range(1, 102):
        req = Request('https://api.github.com' + path
                      + ('&' if '?' in path else '?')
                      + 'per_page=100&page=' + str(page),
                      headers={'Authorization': 'Bearer ' + token,
                               'Accept': 'application/vnd.github+json',
                               'X-GitHub-Api-Version': '2022-11-28'})
        try:
            with urlopen(req, timeout=20) as resp:
                result = json.load(resp)
        except (HTTPError, URLError, ValueError, TimeoutError) as exc:
            raise HandoffError('GitHub retrieval failed: ' + str(exc)) from exc
        if isinstance(result, dict) and page == 1:
            return result
        if not isinstance(result, list):
            raise HandoffError('Unexpected GitHub response type')
        results.extend(result)
        if len(result) < 100:
            return results
    raise HandoffError('GitHub pagination exceeded safe limit')
