"""Restricted GitHub and Dropbox adapters for trusted PR admission."""
import base64
import binascii
import json
import sys
import re
from urllib.error import HTTPError, URLError
from urllib.request import Request
from scripts.admission_metadata import AdmissionError, REFERENCE, SHA, metadata_reference, validate_record, event_pr_number
from scripts.admission_review import agent_review_reference, validate_agent_review
from scripts.admission_candidate import verify_candidate, verify_files, candidate_snapshot, verify_required_checks
from scripts.admission_authentication import authenticate_review
from scripts.admission_transport import open_no_redirect, strict_json, response_json
from scripts.review_execution import validate_review_scope

class GitHub:
    def __init__(self, repository, token):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
            raise AdmissionError("invalid repository")
        if not token:
            raise AdmissionError("missing GitHub token")
        self.base = "https://api.github.com/repos/" + repository
        self.repository = repository
        self.token = token
    def request(self, method, path, body=None):
        if method != "GET" or body is not None:
            raise AdmissionError("GitHub reader permits GET requests only")
        request = Request(
            self.base + path,
            method=method,
            headers={"Authorization": "Bearer " + self.token,
                     "Accept": "application/vnd.github+json",
                     "X-GitHub-Api-Version": "2022-11-28",
                     "Content-Type": "application/json"},
        )
        try:
            with open_no_redirect(request) as response:
                return response_json(response)
        except (HTTPError, URLError, ValueError, TimeoutError) as error:
            raise AdmissionError("GitHub API unavailable or rejected request") from error
    def pages(self, path):
        items = []
        for page in range(1, 11):
            suffix = ("&" if "?" in path else "?") + f"per_page=100&page={page}"
            chunk = self.request("GET", path + suffix)
            if not isinstance(chunk, list):
                raise AdmissionError("invalid GitHub list response")
            items.extend(chunk)
            if len(chunk) < 100:
                return items
        raise AdmissionError("GitHub pagination exceeds safety bound")
    def commit_files(self, sha):
        files = []
        for page in range(1, 11):
            record = self.request("GET", f"/commits/{sha}?per_page=100&page={page}")
            chunk = record.get("files")
            if record.get("sha") != sha or not isinstance(chunk, list):
                raise AdmissionError("incomplete GitHub commit file evidence")
            files.extend(chunk)
            if len(chunk) < 100:
                return files
        raise AdmissionError("GitHub commit files exceed page bound")
    def blob(self, sha):
        record = self.request("GET", "/git/blobs/" + sha)
        if record.get("encoding") != "base64":
            raise AdmissionError("GitHub blob encoding unavailable")
        try:
            encoded = record["content"].replace("\n", "").replace("\r", "")
            return base64.b64decode(encoded, validate=True)
        except (KeyError, AttributeError, TypeError, ValueError, binascii.Error) as error:
            raise AdmissionError("invalid GitHub blob") from error
    def pr(self, number):
        return self.request("GET", f"/pulls/{number}")
    def check_runs(self, sha, page=1):
        if not SHA.fullmatch(str(sha)) or not isinstance(page, int) or page < 1:
            raise AdmissionError("invalid check-runs request")
        record = self.request("GET", f"/commits/{sha}/check-runs?per_page=100&page={page}")
        runs = record.get("check_runs")
        if not isinstance(runs, list):
            raise AdmissionError("invalid GitHub check-runs response")
        return runs

class Dropbox:
    """Read-only Dropbox evidence download; no PR-controlled commands or URLs."""
    def __init__(self, token):
        if not token:
            raise AdmissionError("Dropbox read token not configured")
        self.token = token
    def record(self, reference):
        if not REFERENCE.fullmatch(reference):
            raise AdmissionError("invalid Dropbox record path")
        path = reference.removeprefix("dropbox:")
        request = Request(
            "https://content.dropboxapi.com/2/files/download",
            headers={"Authorization": "Bearer " + self.token,
                     "Dropbox-API-Arg": json.dumps({"path": path}),
                     "Content-Type": "application/octet-stream"},
        )
        try:
            with open_no_redirect(request) as response:
                metadata = strict_json(response.headers["Dropbox-API-Result"])
                if metadata.get("path_display", "").casefold() != path.casefold():
                    raise AdmissionError("Dropbox read-back path differs from reference")
                if metadata.get(".tag") != "file" or not metadata.get("id"):
                    raise AdmissionError("Dropbox record is not a downloadable file")
                content = response.read(131073)
                if len(content) > 131072:
                    raise AdmissionError("Dropbox record exceeds 128KiB bound")
                return strict_json(content)
        except AdmissionError:
            raise
        except (HTTPError, URLError, ValueError, KeyError, UnicodeError, TimeoutError) as error:
            raise AdmissionError("Dropbox record missing, invalid or inaccessible") from error


def record_readset(fetch):
    """Track exact JSON values and re-read them before the decision."""
    observed = {}
    def read(reference):
        value = fetch(reference)
        encoded = json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False)
        previous = observed.setdefault(reference, encoded)
        if previous != encoded:
            raise AdmissionError("Dropbox evidence changed during admission")
        observed[reference] = encoded
        return value
    def recheck():
        for reference in tuple(observed):
            read(reference)
    return read, recheck

def execute(event, github, dropbox_token, publisher, review_key, execution_key, clock,
            required_checks):
    """Authenticate evidence before asking a separate trusted issuer for success.

    Caller supplies qualified public authorities and an isolated status publisher.
    `required_checks` is trusted configuration naming the CI checks that must
    have succeeded on the exact candidate head; it is never read from PR data.
    This validation has no merge-time reservation or exclusive merge capability.
    """
    if not all((callable(getattr(publisher, "status", None)), publisher is not github, callable(clock))):
        raise AdmissionError("separate qualified publisher and clock required")
    number = event_pr_number(event)
    pr = github.pr(number)
    initial = candidate_snapshot(pr)
    head = (pr.get("head") or {}).get("sha")
    if not SHA.fullmatch(str(head)):
        raise AdmissionError("invalid initial PR head")
    publisher.status(head, "pending", "Checking external evidence and independent review")
    try:
        if pr.get("number") != number:
            raise AdmissionError("GitHub PR response number differs from requested candidate")
        dropbox = Dropbox(dropbox_token) if isinstance(dropbox_token, str) else dropbox_token
        commits = github.pages(f"/pulls/{number}/commits")
        read, recheck = record_readset(dropbox.record)
        reference = agent_review_reference(pr.get("body") or "")
        review = read(reference)
        verify_candidate(pr, review, commits, read, head, github)
        candidate = {"repository": github.repository, "pr_number": number,
                     "head_sha": head, "base_sha": pr["base"]["sha"]}
        authenticate_review(review, reference, candidate, review_key, execution_key, read, int(clock()))
        recheck()
        if candidate_snapshot(github.pr(number)) != initial:
            raise AdmissionError("PR state changed during admission (head changed or other drift)")
        verify_required_checks(head, github, required_checks)
        validate_review_scope(review, candidate, int(clock()))
    except AdmissionError as error:
        publisher.status(head, "failure", str(error))
        raise
    publisher.status(head, "success", "Verified evidence and independent exact-head agent review")

def main():
    print("FAIL: privileged admission requires an explicitly configured trusted runner; "
          "standalone CLI is disabled", file=sys.stderr)
    return 1

if __name__ == "__main__":
    sys.exit(main())
