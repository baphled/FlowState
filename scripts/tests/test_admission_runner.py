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

from scripts.admission_metadata import AdmissionError
from scripts.admission_runner import main, process, report, run_candidate, scan, watch
from scripts.tests.test_admission_io import (
    HEAD, FakeGitHub, FakePublisher, FakeDropbox, REVIEW_PRIVATE, EXECUTION_PRIVATE, public,
)

CONFIG = {'repository': 'baphled/FlowState', 'repository_id': 1191083392,
          'app_id': 5728089, 'installation_id': 181235940, 'private_key': b'synthetic',
          'github_read_token': 'synthetic', 'dropbox_read_token': 'synthetic',
          'review_public_key': 'a' * 64, 'execution_public_key': 'b' * 64}


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
