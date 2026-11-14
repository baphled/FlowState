"""Read-only commit evidence and exact-head PR preflight using admission validators.

Run as python3 -B -m scripts.admission_preflight from the checkout root.
No mode publishes statuses, signs reviews or grants merge authorisation.
"""
import argparse
import os
import subprocess
import sys
import time

from scripts.admission_authentication import authenticate_review
from scripts.admission_candidate import candidate_snapshot, verify_candidate, verify_commit_chain
from scripts.admission_checks import verify_required_checks
from scripts.admission_io import Dropbox, GitHub, record_readset
from scripts.admission_local import CachedRecords, LocalGit, local_commits
from scripts.admission_metadata import AdmissionError
from scripts.admission_review import agent_review_reference
from scripts.review_execution import validate_review_scope


def commit_preflight(base, head, records):
    """Check each commit's parent-bound metadata and complete content manifest."""
    commits = local_commits(base, head)
    read, recheck = record_readset(records.record)
    verify_commit_chain(base, commits, read, LocalGit())
    recheck()


def ready_preflight(number, base, head, github, records, review_key, execution_key, clock):
    """Recheck live PR/evidence/Build and authenticate review without a publisher."""
    pr = github.pr(number)
    initial = candidate_snapshot(pr)
    if pr.get('number') != number or (pr.get('base') or {}).get('sha') != base:
        raise AdmissionError('PR number or base differs from local candidate')
    read, recheck = record_readset(records.record)
    reference = agent_review_reference(pr.get('body') or '')
    review = read(reference)
    commits = github.pages(f'/pulls/{number}/commits')
    verify_candidate(pr, review, commits, read, head, github)
    candidate = {'repository': github.repository, 'pr_number': number,
                 'head_sha': head, 'base_sha': base}
    authenticate_review(review, reference, candidate, review_key, execution_key, read, int(clock()))
    recheck()
    if candidate_snapshot(github.pr(number)) != initial:
        raise AdmissionError('PR state changed during preflight')
    verify_required_checks(head, github, ('Build',))
    validate_review_scope(review, candidate, int(clock()))


def parser():
    arguments = argparse.ArgumentParser(description=__doc__)
    arguments.add_argument('--base', required=True)
    arguments.add_argument('--head', required=True)
    arguments.add_argument('--phase', choices=('commits', 'ready'), default='commits')
    arguments.add_argument('--records', default=os.environ.get('FLOWSTATE_ADMISSION_RECORDS'),
                           help='external JSON reference-to-record map; commits phase only')
    arguments.add_argument('--repo', default='baphled/FlowState')
    arguments.add_argument('--pr', type=int)
    return arguments


def run(args):
    if args.phase == 'commits':
        records = CachedRecords(args.records) if args.records else Dropbox(
            os.environ.get('DROPBOX_READ_TOKEN', ''))
        commit_preflight(args.base, args.head, records)
        return 'Commit evidence preflight passed; review and live admission still required'
    if args.records or not args.pr or args.pr <= 0:
        raise AdmissionError('ready phase requires --pr and live Dropbox reads (no cache)')
    github = GitHub(args.repo, os.environ.get('GITHUB_TOKEN', ''))
    ready_preflight(args.pr, args.base, args.head, github,
                    Dropbox(os.environ.get('DROPBOX_READ_TOKEN', '')),
                    os.environ.get('FLOWSTATE_REVIEW_PUBLIC_KEY', ''),
                    os.environ.get('FLOWSTATE_EXECUTION_PUBLIC_KEY', ''), time.time)
    return 'Read-only PR preflight passed; trusted admission status still required'


def main(argv=None):
    args = parser().parse_args(argv)
    try:
        print(run(args))
        return 0
    except (AdmissionError, OSError, ValueError, subprocess.CalledProcessError) as error:
        print('FAIL: ' + str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
