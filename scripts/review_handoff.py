#!/usr/bin/env python3
"""Fail-closed implementation handoff for one exact FlowState PR candidate.

The coordinator provides a Dropbox read-back JSON ledger at --metadata.
GitHub is the live discussion surface, not proof of independent approval.
"""
import argparse
import json
import os
import re
import sys

from scripts.review_handoff_io import (
    BLOCKING, HandoffError, collect_actionable, evidence_links,
    github_get, parse_ledger,
)

SHA_RE = re.compile(r'^[0-9a-f]{40}$')
REPO_RE = re.compile(r'^[\w.-]+/[\w.-]+$')


def evaluate(pr_data, comments, reviews, inline, ledger, head):
    """Return outstanding corrections; never infer review acceptance."""
    if pr_data.get('head', {}).get('sha') != head:
        raise HandoffError('PR changed while retrieving evidence; retry exact head')
    if pr_data.get('state') != 'open':
        raise HandoffError('Candidate is not an open PR')
    if not all(isinstance(items, list) for items in (comments, reviews, inline)):
        raise HandoffError('Incomplete GitHub review surfaces')
    actionable = collect_actionable(comments, reviews, inline)
    missing = sorted(set(actionable) - set(ledger))
    if missing:
        raise HandoffError('GitHub findings missing from Dropbox ledger: '
                           + ', '.join(missing))
    findings = [
        {'id': fid, 'status': item['status'],
         'description': item.get('description', ''),
         'requested_change': item.get('requested_change', ''),
         'test_oracle': item.get('test_oracle', ''),
         'links': actionable.get(fid, [])}
        for fid, item in sorted(ledger.items()) if item['status'] in BLOCKING
    ]
    unstructured = [r.get('html_url') or 'unknown' for r in reviews
                    if r.get('state', '').upper() == 'CHANGES_REQUESTED'
                    and not evidence_links(r)]
    if unstructured:
        raise HandoffError('Unstructured change requests require triage: '
                           + ', '.join(unstructured))
    return {'pr': pr_data['number'], 'head_sha': head,
            'status': 'changes_required' if findings else 'review_pending',
            'findings': findings, 'review_accepted': False}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', required=True)
    parser.add_argument('--pr', type=int, required=True)
    parser.add_argument('--head', required=True)
    parser.add_argument('--metadata', required=True)
    args = parser.parse_args(argv)
    try:
        if not REPO_RE.fullmatch(args.repo) or args.pr < 1 or not SHA_RE.fullmatch(args.head):
            raise HandoffError('Invalid repo, PR number or full commit SHA')
        with open(args.metadata, encoding='utf-8') as file:
            ledger = parse_ledger(json.load(file), args.pr, args.head)
        api = '/repos/{}/'.format(args.repo)
        token = os.environ.get('GITHUB_TOKEN')
        pr_data = github_get(api + 'pulls/' + str(args.pr), token)
        comments = github_get(api + 'issues/' + str(args.pr) + '/comments', token)
        reviews = github_get(api + 'pulls/' + str(args.pr) + '/reviews', token)
        inline = github_get(api + 'pulls/' + str(args.pr) + '/comments', token)
        report = evaluate(pr_data, comments, reviews, inline, ledger, args.head)
        print(json.dumps(report, indent=2, sort_keys=True))
        return 2 if report['findings'] else 0
    except (OSError, ValueError, HandoffError) as exc:
        print('Review handoff blocked: ' + str(exc), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
