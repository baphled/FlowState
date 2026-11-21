"""Terminal publication, polling and timeout controls with no signing authority."""
import io
import os
import sys
import tempfile
import unittest
import signal
import subprocess
from contextlib import redirect_stdout
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

from scripts.admission_metadata import AdmissionError, AdmissionPending, IN_PROGRESS
from scripts.admission_publish import ChangedStatusPublisher
from scripts.admission_runner import (
    CandidatePublisher, SCAN_FAILURE_LIMIT, main, process, report, run_candidate, scan, serve, watch,
)
from scripts.status_issuer import StatusIssuer
from scripts.tests.test_status_issuer import CONTEXT, protection
from scripts.tests.test_admission_io import (
    HEAD, FakeGitHub, FakePublisher, FakeDropbox, REVIEW_PRIVATE, EXECUTION_PRIVATE, public,
)

CONFIG = {'repository': 'baphled/FlowState', 'repository_id': 1191083392,
          'app_id': 5728089, 'installation_id': 181235940, 'private_key': b'synthetic',
          'github_read_token': 'synthetic', 'dropbox_read_token': 'synthetic',
          'review_public_key': 'a' * 64, 'execution_public_key': 'b' * 64}

WAITING = 'Awaiting an independent exact-head agent review'


def issuing(policy):
    """A real StatusIssuer over synthetic credentials; the list records every POST."""
    posted = []

    def post(method, path, token, body):
        posted.append((body['state'], body['description']))
        return {'state': body['state'], 'context': CONTEXT,
                'creator': {'login': 'trusted[bot]', 'type': 'Bot'}}
    credentials = SimpleNamespace(token='t', app_id=10, bot_login='trusted[bot]',
                                  clock=lambda: 1, expires_at=9, request=post)
    return StatusIssuer('owner/repo', credentials, SimpleNamespace(request=lambda *a: policy)), posted


class Runner(unittest.TestCase):
    def test_valid_candidate_publishes_success_only_after_actual_validation(self):
        github = FakeGitHub()
        config = dict(CONFIG, dropbox_read_token=FakeDropbox(),
                      review_public_key=public(REVIEW_PRIVATE), execution_public_key=public(EXECUTION_PRIVATE))
        self.assertTrue(run_candidate(225, github, FakePublisher(github), config, lambda: 150))
        self.assertEqual([state for _, state, _ in github.statuses], ['pending', 'success'])

    def test_unexpected_error_and_interrupt_publish_failure(self):
        for error in (ValueError('secret must not escape'), KeyboardInterrupt()):
            github = FakeGitHub()
            with patch('scripts.admission_runner.execute', side_effect=error):
                if isinstance(error, KeyboardInterrupt):
                    with self.assertRaises(KeyboardInterrupt):
                        run_candidate(225, github, FakePublisher(github), CONFIG)
                else:
                    self.assertFalse(run_candidate(225, github, FakePublisher(github), CONFIG))
            self.assertEqual(github.statuses[-1][:2], (HEAD, 'failure'))
            self.assertNotIn('secret', github.statuses[-1][2])

    def test_head_drift_cannot_publish_success_for_a_new_head(self):
        github = FakeGitHub(change_head=True)
        def changed(event, reader, token, publisher, *args):
            publisher.status('9' * 40, 'success', 'must reject')
        with patch('scripts.admission_runner.execute', side_effect=changed):
            self.assertFalse(run_candidate(225, github, FakePublisher(github), CONFIG))
        self.assertEqual(github.statuses[-1][:2], (HEAD, 'failure'))

    def test_missing_review_is_processed_and_next_pr_survives_an_exception(self):
        reader = Mock()
        reader.pages.return_value = [{'number': n, 'state': 'open', 'draft': False,
                                     'base': {'ref': 'next'}, 'body': ''} for n in (265, 267)]
        with patch('scripts.admission_runner.GitHub', return_value=reader), \
                patch('scripts.admission_runner.process', side_effect=[RuntimeError(), False]) as run:
            self.assertFalse(scan(CONFIG))
            self.assertEqual([c.args[0] for c in run.call_args_list], [265, 267])

    def test_waiting_refusal_is_published_as_pending_and_other_errors_as_failure(self):
        for error, state in ((AdmissionPending('Awaiting x'), 'pending'), (AdmissionError('bad'), 'failure')):
            github = FakeGitHub()
            with self.subTest(state=state), patch('scripts.admission_runner.execute', side_effect=error):
                self.assertFalse(run_candidate(225, github, FakePublisher(github), CONFIG))
                self.assertEqual(github.statuses[-1], (HEAD, state, str(error)))

    def cycle(self, github, issuer, published):
        config = dict(CONFIG, dropbox_read_token=FakeDropbox(), review_public_key=public(REVIEW_PRIVATE),
                      execution_public_key=public(EXECUTION_PRIVATE))
        publisher = CandidatePublisher(ChangedStatusPublisher(issuer, published), HEAD)
        return run_candidate(225, github, publisher, config, lambda: 150)

    def unreviewed(self):
        github = FakeGitHub()
        original = github.pr
        github.pr = lambda number: dict(original(number), body='')
        return github

    def test_admitted_candidate_publishes_the_marker_then_success_every_cycle(self):
        issuer, posted = issuing(protection())
        github, published = FakeGitHub(), {}
        results = [self.cycle(github, issuer, published) for _ in range(3)]
        self.assertEqual((results, [state for state, _ in posted]), ([True] * 3, ['pending', 'success'] * 3))

    def drifted(self, mutate):
        """Admit a head, apply the drift, run one more cycle; return the statuses of that cycle."""
        policy = protection()
        issuer, posted = issuing(policy)
        github, published = FakeGitHub(), {}
        self.cycle(github, issuer, published)
        before = len(posted)
        mutate(github, policy)
        self.assertFalse(self.cycle(github, issuer, published))
        return [state for state, _ in posted[before:]]

    def test_protection_unpinned_gives_pending_then_failure_in_the_same_cycle(self):
        self.assertEqual(self.drifted(lambda g, p: p['required_status_checks'].update(checks=[])),
                         ['pending', 'failure'])

    def test_review_line_removed_gives_pending_then_waiting_pending_in_the_same_cycle(self):
        def remove(github, policy):
            original = github.pr
            github.pr = lambda number: dict(original(number), body='')
        self.assertEqual(self.drifted(remove), ['pending', 'pending'])

    def test_required_check_turning_red_gives_pending_then_failure_in_the_same_cycle(self):
        self.assertEqual(self.drifted(lambda g, p: g.checks[0].update(conclusion='failure')),
                         ['pending', 'failure'])

    def test_duplicate_review_lines_give_pending_then_failure_in_the_same_cycle(self):
        def duplicate(github, policy):
            original = github.pr
            body = original(225)['body']
            github.pr = lambda number: dict(original(number), body=body + '\n' + body)
        self.assertEqual(self.drifted(duplicate), ['pending', 'failure'])

    def test_unreviewed_candidate_publishes_a_constant_number_of_statuses(self):
        issuer, posted = issuing(protection())
        github, published = self.unreviewed(), {}
        for _ in range(5):
            self.assertFalse(self.cycle(github, issuer, published))
        self.assertEqual(posted, [('pending', IN_PROGRESS), ('pending', WAITING)])

    def test_two_prs_on_one_head_publish_three_statuses_per_steady_cycle_and_the_last_wins(self):
        issuer, posted = issuing(protection())
        admitted, refused, published = FakeGitHub(), self.unreviewed(), {}
        counts = []
        for _ in range(4):
            self.cycle(admitted, issuer, published)
            self.cycle(refused, issuer, published)
            counts.append(len(posted))
        self.assertEqual(([b - a for a, b in zip(counts, counts[1:])], posted[-1][0]), ([3, 3, 3], 'pending'))

    def raising_issuer(self):
        """A StatusIssuer-like fake: records every POST, then raises on `success` (applied, then error)."""
        posted = []

        def status(sha, state, description):
            posted.append((state, description))
            if state == 'success':
                raise AdmissionError('creator mismatch after the POST landed')
        return SimpleNamespace(status=status), posted

    def test_a_success_applied_then_raising_is_followed_by_a_failure_every_cycle(self):
        issuer, posted = self.raising_issuer()
        github, published = FakeGitHub(), {}
        for _ in range(3):
            self.assertFalse(self.cycle(github, issuer, published))
            self.assertEqual(posted[-1][0], 'failure')

    def serving_config(self):
        return dict(CONFIG, dropbox_read_token=FakeDropbox(), review_public_key=public(REVIEW_PRIVATE),
                    execution_public_key=public(EXECUTION_PRIVATE))

    def process_twice(self, published):
        issuer, posted = issuing(protection())
        with patch('scripts.admission_runner.AppCredentials', return_value=Mock()), \
                patch('scripts.admission_runner.StatusIssuer', return_value=issuer):
            for _ in range(2):
                process(225, self.unreviewed(), self.serving_config(), published)
        return posted

    def test_process_publishes_one_verdict_for_an_unreviewed_pr_across_two_calls(self):
        posted = self.process_twice({})
        self.assertEqual(posted, [('pending', IN_PROGRESS), ('pending', WAITING)])

    def test_process_routes_through_the_wrapper_and_fills_the_shared_record(self):
        published = {}
        self.process_twice(published)
        self.assertEqual(published[HEAD][:2], ('pending', WAITING))

    def test_scan_hands_the_shared_record_to_process(self):
        reader = Mock()
        reader.pages.return_value = [{'number': 1, 'state': 'open', 'draft': False, 'base': {'ref': 'next'},
                                      'head': {'sha': HEAD}}]
        published = {}
        with patch('scripts.admission_runner.GitHub', return_value=reader), \
                patch('scripts.admission_runner.process', return_value=True) as processed:
            scan(CONFIG, None, published)
        self.assertIs(processed.call_args.args[3], published)

    def test_scan_forgets_heads_that_are_no_longer_open_candidates(self):
        reader = Mock()
        reader.pages.return_value = [{'number': 1, 'state': 'open', 'draft': False, 'base': {'ref': 'next'},
                                      'head': {'sha': HEAD}}]
        published = {HEAD: ('failure', 'x'), '9' * 40: ('success', 'y')}
        with patch('scripts.admission_runner.GitHub', return_value=reader), \
                patch('scripts.admission_runner.process', return_value=True):
            scan(CONFIG, None, published)
        self.assertEqual(list(published), [HEAD])

    def serve(self, scan_effects, sleeps):
        errors = io.StringIO()
        with patch('scripts.admission_runner.configuration', return_value=dict(CONFIG)), \
                patch('scripts.admission_runner.startup'), patch('scripts.admission_runner.ready'), \
                patch('scripts.admission_runner.scan', side_effect=scan_effects) as scanned, \
                patch('scripts.admission_runner.time.sleep', side_effect=sleeps) as slept, \
                patch('sys.stderr', errors):
            code = main([])
        return code, scanned.call_count, slept.call_count, errors.getvalue()

    def test_service_loop_survives_scan_admission_errors_and_counts_them(self):
        code, scans, slept, errors = self.serve(
            [AdmissionError('GitHub API unavailable'), AdmissionError('GitHub API unavailable'), True],
            [None, None, KeyboardInterrupt()])
        self.assertEqual((scans, slept), (3, 3))
        self.assertIn('scan failed (2 consecutive): GitHub API unavailable', errors)

    def test_service_survives_nineteen_consecutive_scan_failures(self):
        code, scans, slept, _ = self.serve([AdmissionError('x')] * 19, [None] * 18 + [KeyboardInterrupt()])
        self.assertEqual((scans, slept), (19, 19))

    def test_service_exits_nonzero_on_the_twentieth_consecutive_scan_failure(self):
        code, scans, slept, errors = self.serve([AdmissionError('x')] * 25, [None] * 25)
        self.assertEqual((code, scans, slept), (1, SCAN_FAILURE_LIMIT, SCAN_FAILURE_LIMIT - 1))
        self.assertIn('FAIL: 20 consecutive scan failures', errors)

    def test_a_good_scan_resets_the_failure_count_below_the_limit(self):
        effects = [AdmissionError('x')] * 19 + [True] + [AdmissionError('x')] * 19
        code, scans, _, _ = self.serve(effects, [None] * 38 + [KeyboardInterrupt()])
        self.assertEqual(scans, 39)

    def test_scan_failure_count_resets_after_a_good_scan(self):
        _, _, _, errors = self.serve([AdmissionError('x'), True, AdmissionError('x')],
                                     [None, None, KeyboardInterrupt()])
        self.assertEqual(errors.count('(1 consecutive)'), 2)

    def test_serve_gives_every_scan_the_same_record_object(self):
        records = []
        with patch('scripts.admission_runner.scan', side_effect=lambda c, n, p: records.append(p) or True), \
                patch('scripts.admission_runner.time.sleep', side_effect=[None, KeyboardInterrupt()]):
            with self.assertRaises(KeyboardInterrupt):
                serve(CONFIG)
        self.assertEqual((len(records), records[0] is records[1], records[0]), (2, True, {}))

    def test_unexpected_scan_exception_still_stops_the_service(self):
        code, scans, slept, errors = self.serve([RuntimeError('boom')], [])
        self.assertEqual((code, scans, slept), (1, 1, 0))

    def test_single_shot_scan_admission_error_still_exits_nonzero(self):
        with patch('scripts.admission_runner.configuration', return_value=dict(CONFIG)), \
                patch('scripts.admission_runner.startup'), patch('scripts.admission_runner.ready'), \
                patch('scripts.admission_runner.scan', side_effect=AdmissionError('x')) as scanned, \
                patch('sys.stderr', io.StringIO()):
            self.assertEqual(main(['--once']), 1)
        self.assertEqual(scanned.call_count, 1)

    def test_credentials_are_closed_and_cleanup_failure_revokes_success(self):
        credentials = Mock()
        credentials.close.side_effect = OSError()
        issuer = Mock()
        with patch('scripts.admission_runner.AppCredentials', return_value=credentials), \
                patch('scripts.admission_runner.StatusIssuer', return_value=issuer), \
                patch('scripts.admission_runner.run_candidate', return_value=True):
            with self.assertRaisesRegex(AdmissionError, 'cleanup'):
                process(225, FakeGitHub(), CONFIG)
        issuer.status.assert_called_once_with(HEAD, 'failure', 'issuer credential cleanup failed')
        credentials.close.assert_called_once()

    def publisher_status(self, state, login='flowstate-merge-admission[bot]'):
        return {'context': 'flowstate/merge-admission', 'state': state, 'description': 'd-' + state,
                'creator': {'login': login}}

    def test_monitor_passes_for_every_state_the_trusted_publisher_reported(self):
        for state in ('success', 'pending', 'failure', 'error'):
            status = self.publisher_status(state)
            with self.subTest(state=state):
                self.assertEqual(watch(SimpleNamespace(request=lambda *args: [status]), HEAD, timeout=0), status)

    def test_monitor_fails_naming_the_service_when_the_publisher_never_reported(self):
        ticks = iter(range(0, 1000, 60))
        reader = SimpleNamespace(request=lambda *args: [])
        with self.assertRaisesRegex(AdmissionError, 'systemctl --user status flowstate-admission.service'):
            watch(reader, HEAD, timeout=120, clock=lambda: next(ticks), sleep=lambda _: None)

    def test_monitor_ignores_other_contexts_when_the_publisher_never_reported(self):
        other = dict(self.publisher_status('success'), context='ci/build')
        with self.assertRaisesRegex(AdmissionError, 'not reported'):
            watch(SimpleNamespace(request=lambda *args: [other]), HEAD, timeout=0)

    def test_monitor_rejects_a_status_from_any_other_creator(self):
        for state in ('success', 'pending', 'failure'):
            reader = SimpleNamespace(request=lambda *args: [self.publisher_status(state, 'other')])
            with self.subTest(state=state), self.assertRaisesRegex(AdmissionError, 'wrong publisher'):
                watch(reader, HEAD, timeout=0)

    def test_monitor_judges_the_newest_status_which_github_lists_first(self):
        trusted, other = self.publisher_status('pending'), self.publisher_status('success', 'other')
        self.assertEqual(watch(SimpleNamespace(request=lambda *args: [trusted, other]), HEAD, timeout=0), trusted)
        with self.assertRaisesRegex(AdmissionError, 'wrong publisher'):
            watch(SimpleNamespace(request=lambda *args: [other, trusted]), HEAD, timeout=0)

    def test_report_reduces_the_description_to_one_bounded_plain_line(self):
        status = dict(self.publisher_status('failure'), created_at='2026-11-21T10:00:00Z',
                      description='bad\n::error::injected ' + 'x' * 500)
        text = report(status)
        self.assertEqual(text.count('\n'), 1)
        self.assertNotIn('::error::', text)
        self.assertLess(len(text), 420)
        self.assertIn('as of 2026-11-21T10:00:00Z', text)

    def test_monitor_rejects_a_malformed_sha_before_any_request(self):
        reader = Mock()
        with self.assertRaisesRegex(AdmissionError, 'exact candidate SHA'):
            watch(reader, 'main', timeout=0)
        reader.request.assert_not_called()

    def test_watch_command_states_that_the_required_context_decides_and_writes_summary(self):
        status = self.publisher_status('pending')
        with tempfile.TemporaryDirectory() as directory, redirect_stdout(io.StringIO()) as out, \
                patch('scripts.admission_runner.watch', return_value=status), \
                patch.dict(os.environ, {'GITHUB_STEP_SUMMARY': directory + '/summary', 'GITHUB_TOKEN': 'synthetic'}):
            self.assertEqual(main(['--watch', HEAD]), 0)
            summary = Path(directory, 'summary').read_text()
        for text in (out.getvalue(), summary):
            self.assertIn('pending (d-pending)', text)
            self.assertIn('required status context, not this job, decides the merge', text)

    def test_configuration_error_is_nonzero(self):
        self.assertEqual(main(['--check-config', '--config', '/nonexistent/admission.json']), 1)

    def test_monitor_keeps_polling_until_the_publisher_reports(self):
        answers = iter([[], [self.publisher_status('success')]])
        reader = SimpleNamespace(request=lambda *args: next(answers))
        self.assertEqual(watch(reader, HEAD, timeout=10, clock=lambda: 0, sleep=lambda _: None)['state'], 'success')

    def test_monitor_fetches_individual_status_records_with_verified_creator(self):
        def api(method, path):
            self.assertEqual((method, path), ('GET', '/statuses/' + HEAD + '?per_page=100'))
            return [{'context': 'flowstate/merge-admission', 'state': 'success',
                     'creator': {'login': 'flowstate-merge-admission[bot]'}}]
        self.assertEqual(watch(SimpleNamespace(request=api), HEAD, timeout=0)['state'], 'success')

    def test_monitor_rejects_creatorless_combined_status_response(self):
        reader = SimpleNamespace(request=lambda *args: {'sha': HEAD, 'statuses': []})
        with self.assertRaisesRegex(AdmissionError, 'status records unavailable'):
            watch(reader, HEAD, timeout=0)

    def test_sigterm_publishes_failure_and_closes_credentials(self):
        script = '''
import signal
from unittest.mock import Mock, patch
from scripts.admission_runner import process, terminate
from scripts.tests.test_admission_runner import CONFIG
from scripts.tests.test_admission_io import FakeGitHub
signal.signal(signal.SIGTERM, terminate)
issuer = Mock()
issuer.status.side_effect = lambda head, state, text: print(state, flush=True)
credentials = Mock()
credentials.close.side_effect = lambda: print('closed', flush=True)
def evaluate(event, reader, token, publisher, *args):
    publisher.status(reader.pr(225)['head']['sha'], 'pending', 'evaluating')
    signal.pause()
with patch('scripts.admission_runner.AppCredentials', return_value=credentials), patch('scripts.admission_runner.StatusIssuer', return_value=issuer), patch('scripts.admission_runner.execute', side_effect=evaluate):
    process(225, FakeGitHub(), CONFIG)
'''
        child = subprocess.Popen([sys.executable, '-B', '-c', script], stdout=subprocess.PIPE,
                                 stderr=subprocess.PIPE, text=True)
        self.addCleanup(lambda: child.kill() if child.poll() is None else None)
        self.assertEqual(child.stdout.readline().strip(), 'pending')
        child.send_signal(signal.SIGTERM)
        stdout, _ = child.communicate(timeout=5)
        self.assertEqual(stdout.splitlines(), ['failure', 'closed'])
