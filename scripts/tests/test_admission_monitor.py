"""Behavioural specs for the publisher-liveness monitor."""
import unittest
from types import SimpleNamespace
from unittest.mock import Mock

from scripts.admission_metadata import AdmissionError
from scripts.admission_monitor import report, watch

HEAD = '2' * 40


class Monitor(unittest.TestCase):
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
