"""Trusted polling entry point and bounded, read-only CI publication monitor."""
import argparse
import os
import signal
import sys
import time
from pathlib import Path

from scripts.admission_config import configuration
from scripts.admission_io import GitHub, execute
from scripts.admission_local_dropbox import LocalDropbox
from scripts.admission_metadata import AdmissionError, SHA
from scripts.admission_publish import ChangedStatusPublisher, retain_heads
from scripts.admission_service import ready
from scripts.app_credentials import AppCredentials
from scripts.status_issuer import STATUS_CONTEXT, StatusIssuer, verify_status_source

SCAN_FAILURE_LIMIT = 20


class CandidatePublisher:
    """Never let a second PR read publish a result for a different candidate."""
    def __init__(self, publisher, head):
        self.publisher, self.head = publisher, head

    def status(self, head, state, description):
        if head != self.head:
            raise AdmissionError('candidate head changed before admission publication')
        self.publisher.status(head, state, description)


def reason(error):
    """Preserve expected findings, but never print unexpected exception secrets."""
    return str(error) if isinstance(error, AdmissionError) else 'internal admission error: ' + type(error).__name__


def terminate(signum, frame):
    """Route systemd SIGTERM through terminal-failure publication and token cleanup."""
    raise KeyboardInterrupt('admission runner stopping')


def startup(config):
    """Prove the configured App and protected source are usable before service startup."""
    reader = GitHub(config['repository'], config['github_read_token'])
    credentials = AppCredentials(config['repository'], config['repository_id'], config['app_id'],
                                 config['installation_id'], config['private_key'])
    try:
        verify_status_source(reader.request('GET', '/branches/next/protection'), config['app_id'])
    finally:
        credentials.close()


def run_candidate(number, reader, publisher, config, clock=time.time):
    """Publish terminal failure for validation, unexpected exceptions and interrupts."""
    head = reader.pr(number).get('head', {}).get('sha')
    if not SHA.fullmatch(str(head)):
        raise AdmissionError('cannot bind admission result to candidate head')
    try:
        execute({'pull_request': {'number': number}}, reader, config['dropbox_read_token'],
                CandidatePublisher(publisher, head), config['review_public_key'],
                config['execution_public_key'], clock, ('Build',))
        return True
    except BaseException as error:
        publisher.status(head, 'failure', reason(error))
        if not isinstance(error, Exception):
            raise
        print(f'PR #{number} {head}: {reason(error)}', file=sys.stderr, flush=True)
        return False


def process(number, reader, config, published=None):
    """Mint/revoke a status-only App token for each candidate, including failures.

    `published` is the process-lived record of the last status published per head.
    """
    credentials = AppCredentials(config['repository'], config['repository_id'], config['app_id'],
                                 config['installation_id'], config['private_key'])
    publisher = None
    try:
        head = reader.pr(number).get('head', {}).get('sha')
        issuer = StatusIssuer(config['repository'], credentials, reader)
        publisher = CandidatePublisher(ChangedStatusPublisher(issuer, published), head)
        return run_candidate(number, reader, publisher, config)
    finally:
        try:
            credentials.close()
        except Exception as error:
            if publisher is not None:
                publisher.status(publisher.head, 'failure', 'issuer credential cleanup failed')
            raise AdmissionError('issuer credential cleanup failed') from error


def scan(config, number=None, published=None):
    """Evaluate every ready next PR, including those missing review references."""
    reader = GitHub(config['repository'], config['github_read_token'])
    candidates = [reader.pr(number)] if number else reader.pages('/pulls?state=open&base=next')
    retain_heads(published, candidates)
    passed = True
    for pr in candidates:
        if pr.get('state') != 'open' or pr.get('draft') or pr.get('base', {}).get('ref') != 'next':
            if number:
                raise AdmissionError('requested PR is not open, ready and targeting next')
            continue
        try:
            passed = process(pr['number'], reader, config, published) and passed
        except Exception as error:
            print(f"PR #{pr.get('number')}: {reason(error)}", file=sys.stderr, flush=True)
            passed = False
    return passed


def qualified_status(reader, head):
    """Read creator-bearing status records for the exact candidate SHA."""
    response = reader.request('GET', f'/statuses/{head}?per_page=100')
    if not isinstance(response, list):
        raise AdmissionError('publisher status records unavailable for the candidate')
    matches = [s for s in response if s.get('context') == STATUS_CONTEXT]
    status = next(iter(matches), {})
    if status and status.get('creator', {}).get('login') != 'flowstate-merge-admission[bot]':
        raise AdmissionError('admission status came from the wrong publisher')
    return status


def watch(reader, head, timeout=120, clock=time.monotonic, sleep=time.sleep):
    """Monitor publisher liveness for the exact candidate SHA; return its status.

    Any `flowstate/merge-admission` status from the trusted publisher (success,
    pending, failure or error) proves the publisher is running, so it is returned
    and the job passes: the required status context, not this monitor, decides the
    merge. A status from any other creator raises at once, and so does silence
    until the deadline, which means the publisher is probably not running.
    """
    if not SHA.fullmatch(str(head)):
        raise AdmissionError('monitor requires the exact candidate SHA')
    deadline = clock() + timeout
    while True:
        status = qualified_status(reader, head)
        if status:
            return status
        if clock() >= deadline:
            raise AdmissionError('the admission publisher has not reported for commit ' + head +
                                 ' and is probably not running; check '
                                 '`systemctl --user status flowstate-admission.service`')
        sleep(5)


def report(status):
    """Describe a publisher status, stating that the required context decides the merge.

    The description is echoed into the job log and step summary, so it is reduced to
    one bounded line of plain text first: no line can start a workflow command.
    """
    described = ' '.join(str(status.get('description')).split())[:200].replace('::', ': :')
    return ('Admission publisher has reported: flowstate/merge-admission is ' + str(status.get('state')) +
            ' (' + described + ') as of ' + str(status.get('created_at')) + '. The required status '
            'context, not this job, decides the merge.\n')


def serve(config):
    """Scan for ever; a scan AdmissionError is logged and retried, but SCAN_FAILURE_LIMIT in a row ends it.

    Raising lets main() exit non-zero, so systemd restarts the unit, `startup()` verifies
    the App again and a persistent failure shows in the restart counter.
    """
    published, failures = {}, 0
    while True:
        try:
            scan(config, None, published)
            failures = 0
        except AdmissionError as error:
            failures += 1
            print(f'scan failed ({failures} consecutive): {reason(error)}', file=sys.stderr, flush=True)
            if failures >= SCAN_FAILURE_LIMIT:
                raise AdmissionError(f'{failures} consecutive scan failures') from error
        time.sleep(15)


def dropbox_reader(config):
    """Select the Dropbox read adapter: local sync tree when configured, else API token."""
    if 'dropbox_local_path' in config:
        return LocalDropbox(config['dropbox_local_path'])
    return config['dropbox_read_token']


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', default=os.path.expanduser('~/.config/flowstate/admission.json'))
    parser.add_argument('--check-config', action='store_true')
    parser.add_argument('--once', action='store_true')
    parser.add_argument('--pr', type=int)
    parser.add_argument('--watch', metavar='HEAD_SHA')
    args = parser.parse_args(argv)
    try:
        if args.watch:
            text = report(watch(GitHub('baphled/FlowState', os.environ.get('GITHUB_TOKEN', '')), args.watch))
            print(text, end='', flush=True)
            if os.environ.get('GITHUB_STEP_SUMMARY'):
                Path(os.environ['GITHUB_STEP_SUMMARY']).write_text(text)
            return 0
        config = configuration(args.config)
        config['dropbox_read_token'] = dropbox_reader(config)
        startup(config)
        if args.check_config:
            return 0
        ready()
        if args.once or args.pr:
            return int(not scan(config, args.pr))
        return serve(config)
    except (Exception, KeyboardInterrupt) as error:
        if os.environ.get('GITHUB_STEP_SUMMARY'):
            Path(os.environ['GITHUB_STEP_SUMMARY']).write_text('Merge admission failed: ' + reason(error) + '\n')
        print('FAIL: ' + reason(error), file=sys.stderr, flush=True)
        return 1


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, terminate)
    raise SystemExit(main())
