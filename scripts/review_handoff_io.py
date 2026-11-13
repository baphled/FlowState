"""Fetch review surfaces and validate externally materialised review findings."""
import re

ID_RE = re.compile(r'\bF\d+-\d{2,}\b')
BLOCKING = frozenset(('open', 'changes_required', 'reopened'))
CLOSED = frozenset(('fixed', 'rejected', 'deferred', 'accepted'))


class HandoffError(RuntimeError):
    """Fail-closed GitHub or Dropbox review evidence failure."""


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


def blocking_findings(ledger, actionable):
    """Project outstanding ledger findings with their GitHub source references."""
    return [
        {'id': fid, 'status': item['status'],
         'description': item.get('description', ''),
         'requested_change': item.get('requested_change', ''),
         'test_oracle': item.get('test_oracle', ''),
         'links': actionable.get(fid, [])}
        for fid, item in sorted(ledger.items()) if item['status'] in BLOCKING
    ]


def unstructured_changes(comments, reviews):
    """Surface old-style blocking conversation and review comments for triage."""
    found = []
    for comment in comments:
        if isinstance(comment, dict) and re.search(
                r'(?i)\bchanges required\b', comment.get('body') or '') \
                and not evidence_links(comment):
            found.append((comment.get('html_url') or 'unknown') + ': '
                         + (comment.get('body') or '')[:300])
    for item in reviews:
        if item.get('state', '').upper() == 'CHANGES_REQUESTED' \
                and not evidence_links(item):
            found.append(item.get('html_url') or 'unknown')
    return found
