"""Protected status-source checks with synthetic App credentials."""
import unittest
from types import SimpleNamespace
from unittest.mock import Mock

from scripts.admission_metadata import AdmissionError
from scripts.status_issuer import StatusIssuer, verify_status_source

SHA = 'a' * 40
CONTEXT = 'flowstate/merge-admission'


def protection():
    return {'enforce_admins': {'enabled': True},
            'required_linear_history': {'enabled': True},
            'required_status_checks': {'strict': True, 'checks': [
                {'context': 'Build', 'app_id': 15368},
                {'context': CONTEXT, 'app_id': 10}]}}


class StatusIssuerTests(unittest.TestCase):
    def setUp(self):
        self.policy = protection()
        self.reader = SimpleNamespace(request=Mock(return_value=self.policy))
        self.credentials = SimpleNamespace(
            token='synthetic-status-token', app_id=10, bot_login='trusted-admission[bot]',
            clock=lambda: 100, expires_at=200,
            request=Mock(return_value={'state': 'success', 'context': CONTEXT,
                                       'creator': {'login': 'trusted-admission[bot]', 'type': 'Bot'}}))
        self.issuer = StatusIssuer('owner/repo', self.credentials, self.reader)

    def test_success_requires_live_pinned_app_and_preserved_protection(self):
        self.issuer.status(SHA, 'success', 'verified')
        self.reader.request.assert_called_once_with('GET', '/branches/next/protection')
        self.credentials.request.assert_called_once_with(
            'POST', '/repos/owner/repo/statuses/' + SHA, 'synthetic-status-token',
            {'state': 'success', 'context': CONTEXT, 'description': 'verified'})

    def test_missing_wrong_unpinned_duplicate_source_never_publishes(self):
        checks = self.policy['required_status_checks']['checks']
        for source in ([], [{'context': CONTEXT, 'app_id': None}],
                       [{'context': CONTEXT, 'app_id': -1}],
                       [{'context': CONTEXT, 'app_id': 11}], checks + [checks[-1]]):
            self.policy['required_status_checks']['checks'] = source
            with self.subTest(source=source), self.assertRaises(AdmissionError):
                self.issuer.status(SHA, 'success', 'verified')
        self.credentials.request.assert_not_called()

    def test_protection_read_error_blocks_success(self):
        self.reader.request.side_effect = AdmissionError('admin read unavailable')
        with self.assertRaises(AdmissionError):
            self.issuer.status(SHA, 'success', 'verified')
        self.credentials.request.assert_not_called()

    def test_weakened_other_controls_fail_closed(self):
        variants = [None, {}, {'required_status_checks': []},
                    {'required_status_checks': {'strict': False}}, protection(), protection()]
        variants[-2]['enforce_admins']['enabled'] = False
        variants[-1]['required_linear_history']['enabled'] = False
        for policy in variants:
            with self.subTest(policy=policy), self.assertRaises(AdmissionError):
                verify_status_source(policy, 10)

    def test_pending_and_failure_can_revoke_without_success_policy(self):
        for state in ('pending', 'failure'):
            self.credentials.request.return_value['state'] = state
            self.issuer.status(SHA, state, 'failed')
        self.reader.request.assert_not_called()

    def test_expired_revoked_invalid_target_or_state_never_publishes(self):
        for target, state in (('bad', 'success'), (SHA, 'neutral')):
            with self.assertRaises(AdmissionError):
                self.issuer.status(target, state, 'bad')
        self.credentials.expires_at = 100
        with self.assertRaises(AdmissionError):
            self.issuer.status(SHA, 'failure', 'expired')
        self.credentials.expires_at = 200
        self.credentials.token = ''
        with self.assertRaises(AdmissionError):
            self.issuer.status(SHA, 'failure', 'revoked')
        self.credentials.request.assert_not_called()

    def test_publication_errors_or_wrong_creator_are_not_success(self):
        self.credentials.request.side_effect = AdmissionError('publication rejected')
        with self.assertRaises(AdmissionError):
            self.issuer.status(SHA, 'success', 'verified')
        self.credentials.request.side_effect = None
        for response in (None, {}, {'state': 'success', 'context': CONTEXT,
                                     'creator': {'login': 'wrong[bot]', 'type': 'Bot'}}):
            self.credentials.request.return_value = response
            with self.subTest(response=response), self.assertRaises(AdmissionError):
                self.issuer.status(SHA, 'success', 'verified')


if __name__ == '__main__':
    unittest.main()
