"""Publish protected admission statuses using a separate ephemeral App token."""
import re

from scripts.admission_metadata import AdmissionError, SHA

STATUS_CONTEXT = 'flowstate/merge-admission'


def verify_status_source(protection, app_id):
    """Require the live next rule to pin this context to the exact App ID."""
    if not isinstance(protection, dict):
        raise AdmissionError('next branch protection unavailable')
    required = protection.get('required_status_checks') or {}
    if not isinstance(required, dict):
        raise AdmissionError('required status protection malformed')
    checks = required.get('checks') or []
    if not isinstance(checks, list):
        raise AdmissionError('required status checks unavailable')
    sources = [item.get('app_id') for item in checks
               if isinstance(item, dict) and item.get('context') == STATUS_CONTEXT]
    if sources != [app_id] or type(app_id) is not int or app_id <= 0:
        raise AdmissionError('required admission status is not pinned to trusted GitHub App')
    verify_protection_controls(protection, required, checks)


def verify_protection_controls(protection, required, checks):
    """Preserve strict Build, administrator and linear-history protection."""
    if required.get('strict') is not True or not any(
            isinstance(item, dict) and item.get('context') == 'Build' for item in checks):
        raise AdmissionError('strict Build protection required')
    controls = (protection.get('enforce_admins'), protection.get('required_linear_history'))
    if not all(isinstance(value, dict) and value.get('enabled') is True for value in controls):
        raise AdmissionError('administrator and linear history protection required')
class StatusIssuer:
    """Write only admission statuses; the independent read client has no key."""
    def __init__(self, repository, credentials, reader):
        if not isinstance(repository, str) or not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository):
            raise AdmissionError('invalid trusted repository')
        self.repository = repository
        self.credentials = credentials
        self.reader = reader

    def status(self, sha, state, description):
        """Fail on unsafe targets, expiry, wrong issuer or failed publication."""
        if not isinstance(sha, str) or not SHA.fullmatch(sha) or state not in ('pending', 'success', 'failure'):
            raise AdmissionError('unsafe admission status target')
        credentials = self.credentials
        if not credentials.token or credentials.clock() >= credentials.expires_at:
            raise AdmissionError('installation status credential expired or revoked')
        if state == 'success':
            protection = self.reader.request('GET', '/branches/next/protection')
            verify_status_source(protection, credentials.app_id)
        result = credentials.request('POST', f'/repos/{self.repository}/statuses/{sha}',
                                     credentials.token,
                                     {'state': state, 'context': STATUS_CONTEXT,
                                      'description': str(description)[:140]})
        if not isinstance(result, dict):
            raise AdmissionError('status publication response missing')
        creator = result.get('creator') or {}
        if not isinstance(creator, dict):
            raise AdmissionError('status creator malformed')
        expected = (state, STATUS_CONTEXT, credentials.bot_login, 'Bot')
        actual = (result.get('state'), result.get('context'), creator.get('login'), creator.get('type'))
        if actual != expected:
            raise AdmissionError('status publication issuer or result differs from trusted App')
