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
from scripts.admission_service import ready
from scripts.app_credentials import AppCredentials
from scripts.status_issuer import STATUS_CONTEXT, StatusIssuer, verify_status_source


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


def process(number, reader, config):
    """Mint/revoke a status-only App token for each candidate, including failures."""
    credentials = AppCredentials(config['repository'], config['repository_id'], config['app_id'],
                                 config['installation_id'], config['private_key'])
    publisher = None
    try:
        head = reader.pr(number).get('head', {}).get('sha')
        publisher = CandidatePublisher(StatusIssuer(config['repository'], credentials, reader), head)
        return run_candidate(number, reader, publisher, config)
    finally:
        try:
            credentials.close()
        except Exception as error:
            if publisher is not None:
                publisher.status(publisher.head, 'failure', 'issuer credential cleanup failed')
            raise AdmissionError('issuer credential cleanup failed') from error


def scan(config, number=None):
    """Evaluate every ready next PR, including those missing review references."""
    reader = GitHub(config['repository'], config['github_read_token'])
    candidates = [reader.pr(number)] if number else reader.pages('/pulls?state=open&base=next')
    passed = True
    for pr in candidates:
        if pr.get('state') != 'open' or pr.get('draft') or pr.get('base', {}).get('ref') != 'next':
            if number:
                raise AdmissionError('requested PR is not open, ready and targeting next')
            continue
        try:
            passed = process(pr['number'], reader, config) and passed
        except Exception as error:
            print(f"PR #{pr.get('number')}: {reason(error)}", file=sys.stderr, flush=True)
            passed = False
    return passed


def qualified_status(reader, head):
    """The one admission status for the head, from the qualified publisher only."""
    response = reader.request('GET', f'/commits/{head}/status')
    if response.get('sha') != head or not isinstance(response.get('statuses'), list):
        raise AdmissionError('publisher status response does not bind the candidate')
    matches = [s for s in response['statuses'] if s.get('context') == STATUS_CONTEXT]
    status = matches[0] if matches else {}
    if status and status.get('creator', {}).get('login') != 'flowstate-merge-admission[bot]':
        raise AdmissionError('admission status came from the wrong publisher')
    return status


def watch(reader, head, timeout=120, clock=time.monotonic, sleep=time.sleep):
    """Fail CI explicitly when the qualified publisher is missing, pending or red."""
    if not SHA.fullmatch(str(head)):
        raise AdmissionError('monitor requires the exact candidate SHA')
    deadline = clock() + timeout
    while True:
        status = qualified_status(reader, head)
        if status.get('state') in ('failure', 'error') and clock() >= deadline:
            raise AdmissionError('admission rejected: ' + str(status.get('description')))
        if status.get('state') == 'success':
            return
        if clock() >= deadline:
            raise AdmissionError('admission publisher missing or stuck pending for candidate ' + head)
        sleep(5)


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
            watch(GitHub('baphled/FlowState', os.environ.get('GITHUB_TOKEN', '')), args.watch)
            return 0
        config = configuration(args.config)
        if 'dropbox_local_path' in config:
            config['dropbox_read_token'] = LocalDropbox(config['dropbox_local_path'])
        startup(config)
        if args.check_config:
            return 0
        ready()
        while True:
            passed = scan(config, args.pr)
            if args.once or args.pr:
                return int(not passed)
            time.sleep(15)
    except (Exception, KeyboardInterrupt) as error:
        if os.environ.get('GITHUB_STEP_SUMMARY'):
            Path(os.environ['GITHUB_STEP_SUMMARY']).write_text('Merge admission failed: ' + reason(error) + '\n')
        print('FAIL: ' + reason(error), file=sys.stderr, flush=True)
        return 1


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, terminate)
    raise SystemExit(main())
