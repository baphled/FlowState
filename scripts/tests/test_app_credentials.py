"""Synthetic GitHub App credentials and least-privilege regression tests."""
import base64
import json
import io
import unittest
from datetime import datetime, timezone
from unittest.mock import Mock, patch

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding, rsa

from scripts.admission_metadata import AdmissionError
from scripts.app_credentials import AppCredentials, app_jwt, github_request

NOW = 1800000000


def private_key():
    return rsa.generate_private_key(public_exponent=65537, key_size=2048)


def pem(key):
    return key.private_bytes(serialization.Encoding.PEM,
                            serialization.PrivateFormat.PKCS8,
                            serialization.NoEncryption())


def decode(segment):
    return base64.urlsafe_b64decode(segment + '=' * (-len(segment) % 4))


class AppCredentialTests(unittest.TestCase):
    def setUp(self):
        self.key = private_key()
        self.request = Mock(side_effect=self.response)
        self.token_response = {
            'token': 'synthetic-installation-token',
            'expires_at': datetime.fromtimestamp(NOW + 3600, timezone.utc).isoformat(),
            'permissions': {'statuses': 'write', 'metadata': 'read'},
            'repositories': [{'id': 42, 'full_name': 'owner/repo'}],
        }

    def response(self, method, path, token, body=None):
        if path == '/app':
            return {'id': 10, 'slug': 'trusted-admission'}
        if path == '/repos/owner/repo/installation':
            return {'id': 20, 'app_id': 10}
        return self.token_response

    def credentials(self, **changes):
        arguments = {'repository': 'owner/repo', 'repository_id': 42,
                     'app_id': 10, 'installation_id': 20,
                     'private_key': pem(self.key), 'request': self.request,
                     'clock': lambda: NOW}
        arguments.update(changes)
        return AppCredentials(**arguments)

    def test_jwt_is_rs256_signed_with_short_lifetime(self):
        encoded = app_jwt(10, pem(self.key), NOW)
        header, payload, signature = encoded.split('.')
        self.assertEqual(json.loads(decode(header)), {'alg': 'RS256', 'typ': 'JWT'})
        self.assertEqual(json.loads(decode(payload)), {'iat': NOW - 60, 'exp': NOW + 540, 'iss': '10'})
        self.key.public_key().verify(decode(signature), (header + '.' + payload).encode(),
                                     padding.PKCS1v15(), hashes.SHA256())

    def test_each_execution_requests_new_repository_scoped_status_token(self):
        first = self.credentials()
        second = self.credentials()
        posts = [c for c in self.request.call_args_list if c.args[0] == 'POST']
        self.assertEqual(len(posts), 2)
        self.assertEqual(posts[0].args[3], {'repository_ids': [42], 'permissions': {'statuses': 'write'}})
        self.assertEqual(first.token, second.token)
        self.assertEqual(first.bot_login, 'trusted-admission[bot]')

    def test_missing_or_invalid_credentials_fail_before_http(self):
        for changes in ({'private_key': b''}, {'private_key': b'bad'},
                        {'repository': 'https://evil/repo'}, {'app_id': True},
                        {'installation_id': 0}, {'repository_id': -1}):
            with self.subTest(changes=changes), self.assertRaises(AdmissionError):
                self.credentials(**changes)
        self.request.assert_not_called()

    def test_wrong_app_or_installation_fails(self):
        for path, response in (('/app', {'id': 11, 'slug': 'wrong'}),
                               ('/repos/owner/repo/installation', {'id': 21, 'app_id': 10})):
            original = self.response
            self.request.side_effect = lambda method, target, token, body=None: (
                response if target == path else original(method, target, token, body))
            with self.subTest(path=path), self.assertRaises(AdmissionError):
                self.credentials()

    def test_wrong_repository_expiry_and_excess_permissions_fail(self):
        changes = [ {'repositories': [{'id': 43, 'full_name': 'owner/other'}]},
                    {'permissions': {'statuses': 'write', 'contents': 'write'}},
                    {'permissions': {'statuses': 'read'}}, {'token': ''},
                    {'repositories': [None]},
                    {'expires_at': 'invalid'},
                    {'expires_at': datetime.fromtimestamp(NOW - 1, timezone.utc).isoformat()},
                    {'expires_at': datetime.fromtimestamp(NOW + 7200, timezone.utc).isoformat()} ]
        for update in changes:
            with self.subTest(update=update), self.assertRaises(AdmissionError):
                previous = dict(self.token_response)
                self.token_response.update(update)
                try:
                    self.credentials()
                finally:
                    self.token_response = previous

    def test_malformed_identity_responses_fail_closed(self):
        for value in (None, [], 'wrong'):
            self.request.side_effect = lambda *args: value
            with self.subTest(value=value), self.assertRaises(AdmissionError):
                self.credentials()

    def test_http_uses_fixed_origin_and_bearer_auth(self):
        response = io.BytesIO(b'{"token":"synthetic"}')
        response.status = 201
        with patch('scripts.app_credentials.build_opener') as builder:
            opened = builder.return_value.open
            opened.return_value = response
            result = github_request('POST', '/app/installations/20/access_tokens', 'synthetic-jwt', {})
        request = opened.call_args.args[0]
        self.assertEqual(request.full_url, 'https://api.github.com/app/installations/20/access_tokens')
        self.assertEqual(request.get_header('Authorization'), 'Bearer synthetic-jwt')
        self.assertEqual(result, {'token': 'synthetic'})

    def test_http_rejection_and_invalid_json_have_controlled_errors(self):
        from urllib.error import URLError
        with patch('scripts.app_credentials.build_opener') as builder:
            builder.return_value.open.side_effect = URLError('rejected')
            with self.assertRaisesRegex(AdmissionError, 'unavailable or rejected'):
                github_request('GET', '/app', 'synthetic-jwt')
        response = io.BytesIO(b'not-json')
        response.status = 200
        with patch('scripts.app_credentials.build_opener') as builder:
            builder.return_value.open.return_value = response
            with self.assertRaisesRegex(AdmissionError, 'unavailable or rejected'):
                github_request('GET', '/app', 'synthetic-jwt')

    def test_revoke_uses_installation_token(self):
        credentials = self.credentials()
        credentials.close()
        self.request.assert_called_with('DELETE', '/installation/token', 'synthetic-installation-token')
        self.assertEqual(credentials.token, '')

    def test_unacceptable_minted_credential_is_revoked(self):
        self.token_response['permissions']['contents'] = 'write'
        with self.assertRaises(AdmissionError):
            self.credentials()
        self.assertEqual(self.request.call_args.args,
                         ('DELETE', '/installation/token', 'synthetic-installation-token'))

    def test_redirect_handler_refuses_every_redirect(self):
        from scripts.app_credentials import RejectRedirect
        from urllib.request import Request
        handler = RejectRedirect()
        request = Request('https://api.github.com/app', headers={'Authorization': 'Bearer synthetic'})
        self.assertIsNone(handler.redirect_request(request, None, 302, '', {}, 'https://evil.example'))

    def test_revocation_failure_preserves_token_for_retry(self):
        credentials = self.credentials()
        self.request.side_effect = AdmissionError('transient revoke failure')
        with self.assertRaises(AdmissionError):
            credentials.close()
        self.assertEqual(credentials.token, 'synthetic-installation-token')


if __name__ == '__main__':
    unittest.main()
