"""Terminal publication, polling and timeout controls with no signing authority."""
import sys
import unittest
import signal
import subprocess
from types import SimpleNamespace
from unittest.mock import Mock, patch

from scripts.admission_metadata import AdmissionError
from scripts.admission_runner import main, process, run_candidate, scan, watch
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

    def test_monitor_accepts_only_terminal_trusted_success(self):
        for state in ('failure', 'pending', 'missing', 'wrong', 'success'):
            status = {'context': 'flowstate/merge-admission', 'state': state,
                      'creator': {'login': 'flowstate-merge-admission[bot]'}}
            if state == 'wrong': status['creator']['login'] = 'other'
            reader = SimpleNamespace(request=lambda *args:
                [] if state == 'missing' else [status])
            with self.subTest(state=state):
                if state == 'success':
                    self.assertIsNone(watch(reader, HEAD, timeout=0))
                else:
                    with self.assertRaises(AdmissionError): watch(reader, HEAD, timeout=0)

    def test_configuration_error_is_nonzero(self):
        self.assertEqual(main(['--check-config', '--config', '/nonexistent/admission.json']), 1)

    def test_prior_build_failure_can_be_superseded_by_fresh_success(self):
        statuses = iter(['failure', 'success'])
        reader = SimpleNamespace(request=lambda *args: [{
            'context': 'flowstate/merge-admission', 'state': next(statuses),
            'creator': {'login': 'flowstate-merge-admission[bot]'}}])
        self.assertIsNone(watch(reader, HEAD, timeout=10, clock=lambda: 0, sleep=lambda _: None))

    def test_monitor_fetches_individual_status_records_with_verified_creator(self):
        def api(method, path):
            self.assertEqual((method, path), ('GET', '/statuses/' + HEAD + '?per_page=100'))
            return [{'context': 'flowstate/merge-admission', 'state': 'success',
                     'creator': {'login': 'flowstate-merge-admission[bot]'}}]
        self.assertIsNone(watch(SimpleNamespace(request=api), HEAD, timeout=0))

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
