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
from scripts.admission_metadata import AdmissionError, SHA, refusal_state
from scripts.admission_publish import ChangedStatusPublisher, PublishedRecords, retain_heads
from scripts.admission_monitor import report, watch
from scripts.admission_service import ready
from scripts.app_credentials import AppCredentials
from scripts.status_issuer import StatusIssuer, verify_status_source

SCAN_FAILURE_LIMIT = 20
REASON_LIMIT = 200


class CandidatePublisher:
    """Never let a second PR read publish a result for a different candidate.

    `outcome` is the state the wrapped publisher reports as standing on the head after the newest
    call: the substituted state when it replaced the requested one, None when it published nothing
    or reported nothing. A publisher that returns nothing therefore never counts as a success.
    """
    def __init__(self, publisher, head):
        self.publisher, self.head, self.outcome = publisher, head, None

    def status(self, head, state, description):
        if head != self.head:
            raise AdmissionError('candidate head changed before admission publication')
        self.outcome = None
        self.outcome = self.publisher.status(head, state, description)
        return self.outcome


def reason(error):
    """Preserve expected findings, but never print unexpected exception secrets.

    The text becomes one bounded line of printable characters (REASON_LIMIT), so a finding that
    quotes PR content cannot forge log lines or start a workflow command.
    """
    text = str(error) if isinstance(error, AdmissionError) else 'internal admission error: ' + type(error).__name__
    return ' '.join(''.join(c if c.isprintable() else ' ' for c in text).split())[:REASON_LIMIT]


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
    """Publish terminal failure for validation, unexpected exceptions and interrupts.

    Return True only when the evaluated success is what stands on the head: a failure substituted
    by the publisher (exhausted head, unknown status count) or no publication at all returns False.
    An interrupt (KeyboardInterrupt, SystemExit) is always re-raised after a best-effort failure
    publication, whatever that publication does.
    """
    head = reader.pr(number).get('head', {}).get('sha')
    if not SHA.fullmatch(str(head)):
        raise AdmissionError('cannot bind admission result to candidate head')
    candidate = CandidatePublisher(publisher, head)
    try:
        execute({'pull_request': {'number': number}}, reader, config['dropbox_read_token'],
                candidate, config['review_public_key'], config['execution_public_key'], clock, ('Build',))
        return candidate.outcome == 'success'
    except BaseException as error:
        if isinstance(error, Exception):
            publisher.status(head, refusal_state(error), reason(error))
        else:
            interrupted(publisher, head, error)
            raise
        print(f'PR #{number} {head}: {reason(error)}', file=sys.stderr, flush=True)
        return False


def interrupted(publisher, head, error):
    """Publish the failure for an interrupt; a fault in doing so never replaces the interrupt."""
    try:
        publisher.status(head, 'failure', reason(error))
    except Exception as fault:
        print(f'PR head {head}: interrupt failure not published: {reason(fault)}', file=sys.stderr, flush=True)


def process(number, reader, config, published=None):
    """Mint/revoke a status-only App token for each candidate, including failures.

    `published` is the process-lived PublishedRecords: the last status per head, its status
    count and its waiting clock. It is created here when absent, which loses them after the call.
    """
    credentials = AppCredentials(config['repository'], config['repository_id'], config['app_id'],
                                 config['installation_id'], config['private_key'])
    publisher, interrupt = None, None
    try:
        head = reader.pr(number).get('head', {}).get('sha')
        issuer = StatusIssuer(config['repository'], credentials, reader)
        publisher = CandidatePublisher(ChangedStatusPublisher(issuer, published, reader), head)
        return run_candidate(number, reader, publisher, config)
    except BaseException as error:
        interrupt = None if isinstance(error, Exception) else error
        raise
    finally:
        try:
            credentials.close()
        except Exception as error:
            if interrupt is not None:
                print(f'PR #{number}: issuer credential cleanup failed during an interrupt: {reason(error)}',
                      file=sys.stderr, flush=True)
            else:
                if publisher is not None:
                    publisher.status(publisher.head, 'failure', 'issuer credential cleanup failed')
                raise AdmissionError('issuer credential cleanup failed') from error


def scan(config, number, published):
    """Evaluate every ready next PR, including those missing review references.

    A PR that is not admitted is an ordinary verdict: it was published and process() returned False.
    A candidate whose process() raised is a fault (credentials, GitHub, publication). When there was
    at least one candidate and every one faulted, the scan raises AdmissionError so that serve()
    counts it as failed; one healthy candidate, or none, keeps the scan good.
    """
    reader = GitHub(config['repository'], config['github_read_token'])
    candidates = [reader.pr(number)] if number else reader.pages('/pulls?state=open&base=next')
    retain_heads(published, candidates)
    results, faults = [], 0
    for pr in candidates:
        if pr.get('state') != 'open' or pr.get('draft') or pr.get('base', {}).get('ref') != 'next':
            if number:
                raise AdmissionError('requested PR is not open, ready and targeting next')
            continue
        try:
            results.append(process(pr['number'], reader, config, published))
        except Exception as error:
            print(f"PR #{pr.get('number')}: {reason(error)}", file=sys.stderr, flush=True)
            results.append(False)
            faults += 1
    if faults == len(results) > 0:
        raise AdmissionError(f'every candidate failed in this scan ({faults})')
    return all(results)


def serve(config):
    """Scan for ever; a scan AdmissionError is logged and retried, but SCAN_FAILURE_LIMIT in a row ends it.

    Raising lets main() exit non-zero, so systemd restarts the unit, `startup()` verifies
    the App again and a persistent failure shows in the restart counter.
    """
    published, failures = PublishedRecords(), 0
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
            return int(not scan(config, args.pr, PublishedRecords()))
        return serve(config)
    except (Exception, KeyboardInterrupt) as error:
        if os.environ.get('GITHUB_STEP_SUMMARY'):
            Path(os.environ['GITHUB_STEP_SUMMARY']).write_text('Merge admission failed: ' + reason(error) + '\n')
        print('FAIL: ' + reason(error), file=sys.stderr, flush=True)
        return 1


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, terminate)
    raise SystemExit(main())
