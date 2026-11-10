"""Decode unambiguous JSON and prevent bearer credentials following redirects."""
import json
from urllib.request import HTTPRedirectHandler, build_opener

from scripts.admission_metadata import AdmissionError


class RejectRedirects(HTTPRedirectHandler):
    """Refuse every redirect rather than forwarding credential-bearing requests."""
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def open_no_redirect(request):
    """Open one trusted-origin request; HTTP redirect responses are errors."""
    return build_opener(RejectRedirects()).open(request, timeout=15)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise AdmissionError("duplicate JSON object key")
        result[key] = value
    return result


def reject_constant(value):
    raise AdmissionError("non-finite JSON number is forbidden")


def strict_json(content):
    """Decode evidence without duplicate keys, non-finite numbers or invalid UTF-8."""
    try:
        if isinstance(content, (bytes, bytearray)):
            content = content.decode("utf-8")
        return json.loads(content, object_pairs_hook=unique_object, parse_constant=reject_constant)
    except (ValueError, UnicodeError, RecursionError) as error:
        raise AdmissionError("invalid JSON response") from error


def response_json(response):
    content = response.read(8388609)
    if len(content) > 8388608:
        raise AdmissionError("GitHub response exceeds 8MiB bound")
    return strict_json(content)
