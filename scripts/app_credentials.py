"""Issue repository-scoped, ephemeral GitHub App status credentials."""
import base64
import json
import re
import time
from datetime import datetime
from urllib.error import HTTPError, URLError
from urllib.request import Request, HTTPRedirectHandler, build_opener

from cryptography.exceptions import UnsupportedAlgorithm
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding, rsa

from scripts.admission_metadata import AdmissionError

class RejectRedirect(HTTPRedirectHandler):
    """Never send privileged bearer credentials through an HTTP redirect."""
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def github_request(method, path, token, body=None):
    """Call the fixed GitHub API origin; never return secret-bearing errors."""
    request = Request('https://api.github.com' + path, method=method,
                      data=json.dumps(body).encode() if body is not None else None,
                      headers={'Authorization': 'Bearer ' + token,
                               'Accept': 'application/vnd.github+json',
                               'X-GitHub-Api-Version': '2022-11-28',
                               'Content-Type': 'application/json'})
    try:
        with build_opener(RejectRedirect()).open(request, timeout=15) as response:
            return None if response.status == 204 else json.load(response)
    except (HTTPError, URLError, ValueError, TimeoutError) as error:
        raise AdmissionError('GitHub App request unavailable or rejected') from error


def encoded(value):
    return base64.urlsafe_b64encode(value).rstrip(b'=')


def app_jwt(app_id, private_key, now):
    """Sign a short-lived RS256 App JWT; the publisher alone owns this key."""
    try:
        key = serialization.load_pem_private_key(private_key, password=None)
        if not isinstance(key, rsa.RSAPrivateKey) or key.key_size < 2048:
            raise ValueError('RSA key of at least 2048 bits required')
        header = encoded(b'{"alg":"RS256","typ":"JWT"}')
        payload = encoded(json.dumps({'iat': int(now) - 60, 'exp': int(now) + 540,
                                      'iss': str(app_id)}, separators=(',', ':')).encode())
        message = header + b'.' + payload
        signature = key.sign(message, padding.PKCS1v15(), hashes.SHA256())
        return (message + b'.' + encoded(signature)).decode('ascii')
    except (ValueError, TypeError, UnsupportedAlgorithm) as error:
        raise AdmissionError('GitHub App signing key unavailable or invalid') from error


def validate_token(record, repository, repository_id, now):
    """Reject excess authority, absent repository binding and unsafe expiry."""
    if not isinstance(record, dict) or not isinstance(record.get('token'), str) or not record['token']:
        raise AdmissionError('installation credential missing')
    permissions = record.get('permissions')
    allowed = ({'statuses': 'write'}, {'statuses': 'write', 'metadata': 'read'})
    if permissions not in allowed:
        raise AdmissionError('installation credential has unexpected permissions')
    repos = record.get('repositories')
    if not isinstance(repos, list) or len(repos) != 1:
        raise AdmissionError('installation credential repository scope missing')
    if not isinstance(repos[0], dict):
        raise AdmissionError('installation repository binding malformed')
    if repos[0].get('id') != repository_id or repos[0].get('full_name') != repository:
        raise AdmissionError('installation credential repository mismatch')
    try:
        expiry = datetime.fromisoformat(record['expires_at'].replace('Z', '+00:00'))
        lifetime = expiry.timestamp() - now
        if expiry.tzinfo is None or not 60 < lifetime <= 3600:
            raise ValueError('unsafe expiry')
    except (KeyError, ValueError, TypeError, AttributeError) as error:
        raise AdmissionError('installation credential expiry invalid') from error
    return record['token'], expiry.timestamp()


class AppCredentials:
    """Obtain one execution's status-only token; revoke it during cleanup."""
    def __init__(self, repository, repository_id, app_id, installation_id,
                 private_key, request=github_request, clock=time.time):
        identifiers = (repository_id, app_id, installation_id)
        if any(type(value) is not int or value <= 0 for value in identifiers):
            raise AdmissionError('positive GitHub repository/App/installation IDs required')
        if not isinstance(repository, str) or not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository):
            raise AdmissionError('invalid trusted repository')
        now = clock()
        jwt = app_jwt(app_id, private_key, now)
        app = request('GET', '/app', jwt)
        installation = request('GET', '/repos/' + repository + '/installation', jwt)
        if not isinstance(app, dict) or not isinstance(installation, dict):
            raise AdmissionError('GitHub App identity response malformed')
        if app.get('id') != app_id or not re.fullmatch(r'[a-z0-9-]+', str(app.get('slug', ''))):
            raise AdmissionError('authenticated GitHub App differs from trusted issuer')
        if installation.get('id') != installation_id or installation.get('app_id') != app_id:
            raise AdmissionError('repository installation differs from trusted issuer')
        record = request('POST', f'/app/installations/{installation_id}/access_tokens', jwt,
                         {'repository_ids': [repository_id], 'permissions': {'statuses': 'write'}})
        self.token, self.expires_at = checked_token(record, repository, repository_id, clock(), request)
        self.app_id = app_id
        self.repository = repository
        self.bot_login = app['slug'] + '[bot]'
        self.request = request
        self.clock = clock

    def close(self):
        """Revoke this execution's credential; failed revocation is an error."""
        if self.token:
            self.request('DELETE', '/installation/token', self.token)
            self.token = ''


def checked_token(record, repository, repository_id, now, request):
    """Revoke a minted credential if its returned authority is unacceptable."""
    try:
        return validate_token(record, repository, repository_id, now)
    except AdmissionError:
        token = record.get('token') if isinstance(record, dict) else None
        if isinstance(token, str) and token:
            request('DELETE', '/installation/token', token)
        raise
