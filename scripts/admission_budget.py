"""Bound the statuses one head can receive: GitHub refuses the 1001st per commit and context.

At the limit a `pending` marker or a `failure` is refused, so a standing `success` could no
longer be withdrawn. A head therefore never gets near the limit holding a success.
"""
from scripts.admission_metadata import AdmissionError
from scripts.status_issuer import STATUS_CONTEXT

# GitHub's hard limit per commit SHA and context; the budget leaves room for the final failure and retries.
STATUS_LIMIT = 1000
STATUS_BUDGET = 800
MAX_STATUS_PAGES = 30
BOT_LOGIN = 'flowstate-merge-admission[bot]'
EXHAUSTED = 'Admission window for this commit is exhausted; push a new commit to continue'
UNCOUNTED = 'Admission status count is unavailable; retrying'


def is_final(status):
    """True when this is the exhaustion failure published by the trusted publisher."""
    return bool(status) and (status.get('state'), status.get('description')) == ('failure', EXHAUSTED) \
        and (status.get('creator') or {}).get('login') == BOT_LOGIN


def read_spent(reader, sha):
    """Return (statuses the context holds on the commit, whether the newest is the final failure).

    GitHub lists a commit's statuses newest first with no context filter, so the pages are filtered
    here: one request for a head with fewer than 100 statuses in all, about nine at the budget and
    about eleven at GitHub's limit. A malformed response or more than MAX_STATUS_PAGES pages raises
    AdmissionError.
    """
    count, newest = 0, None
    for page in range(1, MAX_STATUS_PAGES + 1):
        chunk = reader.request('GET', f'/commits/{sha}/statuses?per_page=100&page={page}')
        if not isinstance(chunk, list) or not all(isinstance(item, dict) for item in chunk):
            raise AdmissionError('status records unavailable for the candidate')
        mine = [item for item in chunk if item.get('context') == STATUS_CONTEXT]
        newest = newest or next(iter(mine), None)
        count += len(mine)
        if len(chunk) < 100:
            return count, is_final(newest)
    raise AdmissionError('status records exceed the page bound')
