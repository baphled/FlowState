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
from scripts.admission_budget import EXHAUSTED, STATUS_BUDGET, STATUS_LIMIT, UNCOUNTED
from scripts.admission_publish import ChangedStatusPublisher, PublishedRecords
from scripts.admission_runner import (
    CandidatePublisher, REASON_LIMIT, SCAN_FAILURE_LIMIT, main, process, reason, run_candidate, scan,
    serve,
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

REFERENCE = '/FlowState/Metadata/reviews/x.json'
SPELLING = "review line must be spelled exactly 'Agent-Review: <reference>' at the line start"
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

    def test_an_interrupt_always_escapes_run_candidate_even_when_its_failure_cannot_be_published(self):
        for interrupt in (KeyboardInterrupt(), SystemExit(1)):
            github = FakeGitHub()
            publisher = Mock(status=Mock(side_effect=AdmissionError('publication down')))
            with self.subTest(interrupt=type(interrupt).__name__), \
                    patch('scripts.admission_runner.execute', side_effect=interrupt), \
                    patch('sys.stderr', io.StringIO()), self.assertRaises(type(interrupt)):
                run_candidate(225, github, publisher, CONFIG)

    def test_an_interrupt_escapes_process_even_when_credential_cleanup_fails(self):
        for interrupt in (KeyboardInterrupt(), SystemExit(1)):
            credentials = Mock(close=Mock(side_effect=OSError()))
            reader = FakeGitHub()
            reader.request = lambda method, path: []
            with self.subTest(interrupt=type(interrupt).__name__), \
                    patch('scripts.admission_runner.AppCredentials', return_value=credentials), \
                    patch('scripts.admission_runner.StatusIssuer', return_value=Mock()), \
                    patch('scripts.admission_runner.run_candidate', side_effect=interrupt), \
                    self.assertRaises(type(interrupt)):
                process(225, reader, CONFIG)

    def test_a_credential_cleanup_fault_during_an_interrupt_is_logged(self):
        credentials = Mock(close=Mock(side_effect=OSError()))
        reader = FakeGitHub()
        reader.request = lambda method, path: []
        with patch('scripts.admission_runner.AppCredentials', return_value=credentials), \
                patch('scripts.admission_runner.StatusIssuer', return_value=Mock()), \
                patch('scripts.admission_runner.run_candidate', side_effect=KeyboardInterrupt()), \
                patch('sys.stderr', new_callable=io.StringIO) as err, self.assertRaises(KeyboardInterrupt):
            process(225, reader, CONFIG)
        self.assertIn('cleanup failed during an interrupt', err.getvalue())

    def test_an_interrupt_inside_a_scan_escapes_the_scan(self):
        for interrupt in (KeyboardInterrupt(), SystemExit(1)):
            reader = Mock()
            reader.pages.return_value = [{'number': 1, 'state': 'open', 'draft': False, 'base': {'ref': 'next'},
                                          'head': {'sha': HEAD}}]
            with self.subTest(interrupt=type(interrupt).__name__), \
                    patch('scripts.admission_runner.GitHub', return_value=reader), \
                    patch('scripts.admission_runner.process', side_effect=interrupt), \
                    self.assertRaises(type(interrupt)):
                scan(CONFIG, None, PublishedRecords())

    def test_an_interrupt_inside_a_scan_stops_serve_without_sleeping_or_retrying(self):
        for interrupt in (KeyboardInterrupt(), SystemExit(1)):
            with self.subTest(interrupt=type(interrupt).__name__), \
                    patch('scripts.admission_runner.scan', side_effect=interrupt) as scanned, \
                    patch('scripts.admission_runner.time.sleep') as slept, self.assertRaises(type(interrupt)):
                serve(CONFIG)
            self.assertEqual((scanned.call_count, slept.call_count), (1, 0))

    def test_the_reason_is_one_bounded_line_without_control_characters(self):
        text = reason(AdmissionError('bad\n::error::x\x1b[31m\u2028y\t' + 'z' * 500))
        self.assertEqual((text.isprintable(), len(text) <= REASON_LIMIT), (True, True))

    def test_the_logged_reason_for_a_failed_candidate_is_one_line(self):
        errors = io.StringIO()
        with patch('scripts.admission_runner.execute', side_effect=AdmissionError('bad\n::error::forged')), \
                patch('sys.stderr', errors):
            run_candidate(225, FakeGitHub(), Mock(), CONFIG)
        self.assertEqual(errors.getvalue().count('\n'), 1)

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
            self.assertFalse(scan(CONFIG, None, PublishedRecords()))
            self.assertEqual([c.args[0] for c in run.call_args_list], [265, 267])

    def test_waiting_refusal_is_published_as_pending_and_other_errors_as_failure(self):
        for error, state in ((AdmissionPending('Awaiting x'), 'pending'), (AdmissionError('bad'), 'failure')):
            github = FakeGitHub()
            with self.subTest(state=state), patch('scripts.admission_runner.execute', side_effect=error):
                self.assertFalse(run_candidate(225, github, FakePublisher(github), CONFIG))
                self.assertEqual(github.statuses[-1], (HEAD, state, str(error)))

    def candidate_with_budget(self, read):
        """run_candidate over the real ChangedStatusPublisher with `read` as the status-count read."""
        github, issuer = FakeGitHub(), Mock()
        config = dict(CONFIG, dropbox_read_token=FakeDropbox(), review_public_key=public(REVIEW_PRIVATE),
                      execution_public_key=public(EXECUTION_PRIVATE))
        publisher = ChangedStatusPublisher(issuer, PublishedRecords(), github)
        with patch('scripts.admission_publish.read_spent', side_effect=read):
            return run_candidate(225, github, publisher, config, lambda: 150), issuer

    def test_an_evaluated_success_that_was_published_is_a_success(self):
        admitted, issuer = self.candidate_with_budget([(0, False)])
        self.assertEqual((admitted, issuer.status.call_args.args[1]), (True, 'success'))

    def test_a_success_replaced_by_the_exhaustion_failure_is_not_admitted(self):
        admitted, issuer = self.candidate_with_budget([(STATUS_BUDGET, False)])
        self.assertEqual((admitted, issuer.status.call_args.args[1:]), (False, ('failure', EXHAUSTED)))

    def test_a_success_on_a_terminal_head_publishes_nothing_and_is_not_admitted(self):
        admitted, issuer = self.candidate_with_budget([(STATUS_LIMIT, True)])
        self.assertEqual((admitted, issuer.status.call_count), (False, 0))

    def test_a_success_replaced_by_the_unknown_count_failure_is_not_admitted(self):
        admitted, issuer = self.candidate_with_budget([AdmissionError('GitHub down')])
        self.assertEqual((admitted, issuer.status.call_args.args[1:]), (False, ('failure', UNCOUNTED)))

    def test_a_publisher_that_reports_nothing_never_counts_as_a_success(self):
        github = FakeGitHub()
        publisher = Mock(status=Mock(return_value=None))
        config = dict(CONFIG, dropbox_read_token=FakeDropbox(), review_public_key=public(REVIEW_PRIVATE),
                      execution_public_key=public(EXECUTION_PRIVATE))
        self.assertFalse(run_candidate(225, github, publisher, config, lambda: 150))

    def test_once_exits_nonzero_when_the_publisher_substituted_a_failure(self):
        with patch('scripts.admission_runner.configuration', return_value=dict(CONFIG)), \
                patch('scripts.admission_runner.startup'), patch('scripts.admission_runner.ready'), \
                patch('scripts.admission_runner.GitHub', return_value=Mock(pr=lambda n: {
                    'number': n, 'state': 'open', 'draft': False, 'base': {'ref': 'next'}, 'head': {'sha': HEAD}})), \
                patch('scripts.admission_runner.process', return_value=False):
            self.assertEqual(main(['--once', '--pr', '225']), 1)

    def test_once_exits_zero_when_the_success_was_published(self):
        with patch('scripts.admission_runner.configuration', return_value=dict(CONFIG)), \
                patch('scripts.admission_runner.startup'), patch('scripts.admission_runner.ready'), \
                patch('scripts.admission_runner.GitHub', return_value=Mock(pr=lambda n: {
                    'number': n, 'state': 'open', 'draft': False, 'base': {'ref': 'next'}, 'head': {'sha': HEAD}})), \
                patch('scripts.admission_runner.process', return_value=True):
            self.assertEqual(main(['--once', '--pr', '225']), 0)

    def misspelled(self, line):
        """Run one cycle for a PR whose description carries `line`; return the statuses posted."""
        issuer, posted = issuing(protection())
        github = FakeGitHub()
        original = github.pr
        github.pr = lambda number: dict(original(number), body='Summary\n' + line + '\n')
        self.assertFalse(self.cycle(github, issuer, PublishedRecords()))
        return posted

    def test_a_misspelled_review_line_publishes_a_failure_through_the_runner(self):
        for line in ('agent-review: ' + REFERENCE, 'Agent Review: ' + REFERENCE, 'Agent_Review: ' + REFERENCE,
                     'Agent\u2011Review: ' + REFERENCE, 'Agent\u2013Review: ' + REFERENCE):
            with self.subTest(line=line):
                self.assertEqual(self.misspelled(line), [('pending', IN_PROGRESS), ('failure', SPELLING)])

    def failing_cycles(self, message, cycles=3):
        """Run cycles for a PR whose evaluation fails with `message`; return the statuses posted."""
        issuer, posted = issuing(protection())
        github, published = FakeGitHub(), PublishedRecords()

        def refuse(path):
            raise AdmissionError(message)
        github.pages = refuse
        for _ in range(cycles):
            self.assertFalse(self.cycle(github, issuer, published))
        return posted

    def test_a_long_finding_is_published_once_not_every_cycle(self):
        self.assertEqual(len(self.failing_cycles('Dropbox record missing from external cache: ' + 'x' * 300)), 2)

    def test_a_multi_line_finding_is_published_once_not_every_cycle(self):
        self.assertEqual(len(self.failing_cycles('first line\nsecond line\n\tthird')), 2)

    def test_a_finding_with_control_characters_is_published_once_not_every_cycle(self):
        self.assertEqual(len(self.failing_cycles('bad\x1b[31m text \x00 here')), 2)

    def test_a_short_finding_is_published_once_not_every_cycle(self):
        self.assertEqual(len(self.failing_cycles('record missing')), 2)

    def test_a_published_finding_is_one_printable_line_of_at_most_140_characters(self):
        text = self.failing_cycles('a\nb ' + 'y' * 300, 1)[-1][1]
        self.assertTrue(text.isprintable() and len(text) <= 140)

    def cycle(self, github, issuer, published):
        config = dict(CONFIG, dropbox_read_token=FakeDropbox(), review_public_key=public(REVIEW_PRIVATE),
                      execution_public_key=public(EXECUTION_PRIVATE))
        github.request = lambda method, path: []
        publisher = CandidatePublisher(ChangedStatusPublisher(issuer, published, github), HEAD)
        return run_candidate(225, github, publisher, config, lambda: 150)

    def unreviewed(self):
        github = FakeGitHub()
        original = github.pr
        github.pr = lambda number: dict(original(number), body='')
        github.request = lambda method, path: []
        return github

    def test_admitted_candidate_publishes_the_marker_then_success_every_cycle(self):
        issuer, posted = issuing(protection())
        github, published = FakeGitHub(), PublishedRecords()
        results = [self.cycle(github, issuer, published) for _ in range(3)]
        self.assertEqual((results, [state for state, _ in posted]), ([True] * 3, ['pending', 'success'] * 3))

    def drifted(self, mutate):
        """Admit a head, apply the drift, run one more cycle; return the statuses of that cycle."""
        policy = protection()
        issuer, posted = issuing(policy)
        github, published = FakeGitHub(), PublishedRecords()
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
        github, published = self.unreviewed(), PublishedRecords()
        for _ in range(5):
            self.assertFalse(self.cycle(github, issuer, published))
        self.assertEqual(posted, [('pending', IN_PROGRESS), ('pending', WAITING)])

    def test_two_prs_on_one_head_publish_three_statuses_per_steady_cycle_and_the_last_wins(self):
        issuer, posted = issuing(protection())
        admitted, refused, published = FakeGitHub(), self.unreviewed(), PublishedRecords()
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
        github, published = FakeGitHub(), PublishedRecords()
        for _ in range(3):
            published.reads.clear()  # a scan cycle is 15 s after the last read; the real clock does not advance here
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
        posted = self.process_twice(PublishedRecords())
        self.assertEqual(posted, [('pending', IN_PROGRESS), ('pending', WAITING)])

    def test_process_routes_through_the_wrapper_and_fills_the_shared_record(self):
        published = PublishedRecords()
        self.process_twice(published)
        self.assertEqual(published[HEAD][:2], ('pending', WAITING))

    def test_scan_hands_the_shared_record_to_process(self):
        reader = Mock()
        reader.pages.return_value = [{'number': 1, 'state': 'open', 'draft': False, 'base': {'ref': 'next'},
                                      'head': {'sha': HEAD}}]
        published = PublishedRecords()
        with patch('scripts.admission_runner.GitHub', return_value=reader), \
                patch('scripts.admission_runner.process', return_value=True) as processed:
            scan(CONFIG, None, published)
        self.assertIs(processed.call_args.args[3], published)

    def test_scan_forgets_heads_that_are_no_longer_open_candidates(self):
        reader = Mock()
        reader.pages.return_value = [{'number': 1, 'state': 'open', 'draft': False, 'base': {'ref': 'next'},
                                      'head': {'sha': HEAD}}]
        published = PublishedRecords()
        published.update({HEAD: ('failure', 'x'), '9' * 40: ('success', 'y')})
        published.counts['9' * 40] = 5
        with patch('scripts.admission_runner.GitHub', return_value=reader), \
                patch('scripts.admission_runner.process', return_value=True):
            scan(CONFIG, None, published)
        self.assertEqual(list(published), [HEAD])

    def test_scan_forgets_the_status_count_of_a_head_that_is_no_longer_a_candidate(self):
        reader = Mock()
        reader.pages.return_value = []
        published = PublishedRecords()
        published.counts[HEAD] = 5
        with patch('scripts.admission_runner.GitHub', return_value=reader):
            scan(CONFIG, None, published)
        self.assertEqual(published.counts, {})

    def scanning(self, outcomes):
        """Scan open PRs numbered 1.. whose process() yields each outcome (an exception is raised)."""
        reader = Mock()
        reader.pages.return_value = [{'number': n, 'state': 'open', 'draft': False, 'base': {'ref': 'next'},
                                      'head': {'sha': HEAD}} for n, _ in enumerate(outcomes, 1)]
        with patch('scripts.admission_runner.GitHub', return_value=reader), \
                patch('scripts.admission_runner.process', side_effect=outcomes), patch('sys.stderr', io.StringIO()):
            return scan(CONFIG, None, PublishedRecords())

    def test_a_scan_in_which_every_candidate_faulted_raises(self):
        with self.assertRaises(AdmissionError):
            self.scanning([AdmissionError('credentials'), RuntimeError('boom')])

    def test_a_scan_with_one_candidate_that_faulted_raises(self):
        with self.assertRaises(AdmissionError):
            self.scanning([AdmissionError('credentials')])

    def test_a_scan_with_one_healthy_candidate_is_not_a_failed_scan(self):
        self.assertFalse(self.scanning([AdmissionError('credentials'), True]))

    def test_a_scan_in_which_every_pr_is_simply_not_admitted_is_a_good_scan(self):
        self.assertFalse(self.scanning([False, False]))

    def test_a_scan_with_no_candidates_is_a_good_scan(self):
        self.assertTrue(self.scanning([]))

    def test_twenty_all_faulted_scans_stop_the_service(self):
        reader = Mock()
        reader.pages.return_value = [{'number': 1, 'state': 'open', 'draft': False, 'base': {'ref': 'next'},
                                      'head': {'sha': HEAD}}]
        errors = io.StringIO()
        with patch('scripts.admission_runner.configuration', return_value=dict(CONFIG)), \
                patch('scripts.admission_runner.startup'), patch('scripts.admission_runner.ready'), \
                patch('scripts.admission_runner.GitHub', return_value=reader), \
                patch('scripts.admission_runner.process', side_effect=AdmissionError('credentials')), \
                patch('scripts.admission_runner.time.sleep', side_effect=[None] * 30 + [KeyboardInterrupt()]), \
                patch('sys.stderr', errors):
            code = main([])
        self.assertEqual((code, errors.getvalue().count('scan failed')), (1, SCAN_FAILURE_LIMIT))

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
        issuer, reader = Mock(), FakeGitHub()
        reader.request = lambda method, path: []
        with patch('scripts.admission_runner.AppCredentials', return_value=credentials), \
                patch('scripts.admission_runner.StatusIssuer', return_value=issuer), \
                patch('scripts.admission_runner.run_candidate', return_value=True):
            with self.assertRaisesRegex(AdmissionError, 'cleanup'):
                process(225, reader, CONFIG)
        issuer.status.assert_called_once_with(HEAD, 'failure', 'issuer credential cleanup failed')
        credentials.close.assert_called_once()

    def test_watch_command_states_that_the_required_context_decides_and_writes_summary(self):
        status = {'context': 'flowstate/merge-admission', 'state': 'pending', 'description': 'd-pending',
                  'creator': {'login': 'flowstate-merge-admission[bot]'}}
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

    def test_sigterm_publishes_failure_and_closes_credentials(self):
        script = '''
import signal
from unittest.mock import Mock, patch
from scripts.admission_runner import process, terminate
from scripts.tests.test_admission_runner import CONFIG
from scripts.tests.test_admission_io import FakeGitHub
signal.signal(signal.SIGTERM, terminate)
reader = FakeGitHub()
reader.request = lambda method, path: []
issuer = Mock()
issuer.status.side_effect = lambda head, state, text: print(state, flush=True)
credentials = Mock()
credentials.close.side_effect = lambda: print('closed', flush=True)
def evaluate(event, reader, token, publisher, *args):
    publisher.status(reader.pr(225)['head']['sha'], 'pending', 'evaluating')
    signal.pause()
with patch('scripts.admission_runner.AppCredentials', return_value=credentials), patch('scripts.admission_runner.StatusIssuer', return_value=issuer), patch('scripts.admission_runner.execute', side_effect=evaluate):
    process(225, reader, CONFIG)
'''
        child = subprocess.Popen([sys.executable, '-B', '-c', script], stdout=subprocess.PIPE,
                                 stderr=subprocess.PIPE, text=True)
        self.addCleanup(lambda: child.kill() if child.poll() is None else None)
        self.assertEqual(child.stdout.readline().strip(), 'pending')
        child.send_signal(signal.SIGTERM)
        stdout, _ = child.communicate(timeout=5)
        self.assertEqual(stdout.splitlines(), ['failure', 'closed'])
