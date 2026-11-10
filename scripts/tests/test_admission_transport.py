"""Reject ambiguous JSON and redirect-based credential forwarding."""
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from io import BytesIO
from urllib.error import HTTPError
from urllib.request import Request

from scripts.admission_metadata import AdmissionError
from scripts.admission_transport import strict_json, open_no_redirect, response_json


class EvidenceTransport(unittest.TestCase):
    def test_duplicate_nested_keys_and_non_finite_numbers_fail(self):
        for content in [b'{"x":1,"x":2}',b'{"nested":{"x":1,"x":2}}',
                        b'{"x":NaN}',b'[Infinity]',b'[-Infinity]',b'"\xff"',
                        '{"label":"é"}'.encode('utf-16')]:
            with self.subTest(content=content):
                with self.assertRaises(AdmissionError): strict_json(content)

    def test_unambiguous_unicode_json_succeeds(self):
        self.assertEqual(strict_json('{"label":"é","flag":true}'),{"label":"é","flag":True})

    def test_oversized_response_fails(self):
        with self.assertRaisesRegex(AdmissionError,"8MiB"):
            response_json(BytesIO(b' '*8388609))

    def test_redirect_cannot_forward_bearer_to_another_origin(self):
        calls=[]
        class Target(BaseHTTPRequestHandler):
            def do_GET(self):
                calls.append(self.headers.get('Authorization'))
                self.send_response(200);self.end_headers();self.wfile.write(b'{}')
            def log_message(self,*args): pass
        target=ThreadingHTTPServer(('127.0.0.1',0),Target)
        class Redirect(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(302)
                self.send_header('Location',f'http://127.0.0.1:{target.server_port}/target')
                self.end_headers()
            def log_message(self,*args): pass
        redirect=ThreadingHTTPServer(('127.0.0.1',0),Redirect)
        for server in (target,redirect):
            thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
            self.addCleanup(thread.join)
            self.addCleanup(server.server_close)
            self.addCleanup(server.shutdown)
        request=Request(f'http://127.0.0.1:{redirect.server_port}/',headers={'Authorization':'Bearer synthetic'})
        with self.assertRaises(HTTPError): open_no_redirect(request)
        self.assertEqual(calls,[])
