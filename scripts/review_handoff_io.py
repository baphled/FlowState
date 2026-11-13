"""Fetch review surfaces and validate externally materialised review findings."""
import json
import re
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

ID_RE = re.compile(r'\bF\d+-\d{2,}\b')
BLOCKING = frozenset(('open', 'changes_required', 'reopened'))
CLOSED = frozenset(('fixed', 'rejected', 'deferred', 'accepted'))


class HandoffError(RuntimeError):
    """Fail-closed GitHub or Dropbox review evidence failure."""


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


def parse_ledger(raw, pr, head):
    """Validate PR/head identity, stable IDs and evidence-backed dispositions."""
    if not isinstance(raw, dict) or raw.get('pr') != pr:
        raise HandoffError('Dropbox review ledger PR does not match')
    if raw.get('head_sha') != head:
        raise HandoffError('Dropbox review ledger head is stale; requalify')
    findings = raw.get('findings')
    if not isinstance(findings, list):
        raise HandoffError('Dropbox review ledger must contain findings')
    output = {}
    for item in findings:
        if not isinstance(item, dict):
            raise HandoffError('Malformed review finding')
        fid, state = item.get('id'), item.get('status')
        if not isinstance(fid, str) or not ID_RE.fullmatch(fid):
            raise HandoffError('Invalid stable finding ID')
        if state not in BLOCKING | CLOSED:
            raise HandoffError('Invalid finding state: ' + str(state))
        if fid in output:
            raise HandoffError('Duplicate finding ID: ' + fid)
        if state in CLOSED and not item.get('evidence'):
            raise HandoffError('Resolved finding lacks correction/review evidence: ' + fid)
        output[fid] = item
    return output


def evidence_links(comment):
    """Extract explicit finding IDs without inferring dispositions from prose."""
    return set(ID_RE.findall(comment.get('body') or ''))


def collect_actionable(comments, reviews, inline):
    """Preserve location and origin of each explicitly linked GitHub finding."""
    actionable = {}
    for kind, records in (('conversation', comments), ('review', reviews),
                          ('inline', inline)):
        for item in records:
            if not isinstance(item, dict):
                raise HandoffError('Malformed GitHub review data')
            for fid in evidence_links(item):
                actionable.setdefault(fid, []).append({
                    'surface': kind, 'url': item.get('html_url') or '',
                    'body': item.get('body') or '', 'path': item.get('path'),
                    'line': item.get('line'),
                })
    return actionable
