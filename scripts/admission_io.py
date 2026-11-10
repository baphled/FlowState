"""Restricted GitHub and Dropbox adapters for trusted PR admission."""
import base64
import binascii
import json
import os
import sys
import re
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen
from scripts.admission_metadata import AdmissionError, REFERENCE, SHA, metadata_reference, validate_record, event_pr_number
from scripts.admission_review import agent_review_reference, validate_agent_review
from scripts.admission_candidate import verify_candidate, verify_files, candidate_snapshot

STATUS_CONTEXT = "flowstate/merge-admission"

class GitHub:
    def __init__(self, repository, token):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
            raise AdmissionError("invalid repository")
        if not token:
            raise AdmissionError("missing GitHub token")
        self.base = "https://api.github.com/repos/" + repository
        self.token = token
    def request(self, method, path, body=None):
        request = Request(
            self.base + path,
            data=json.dumps(body).encode() if body is not None else None,
            method=method,
            headers={"Authorization": "Bearer " + self.token,
                     "Accept": "application/vnd.github+json",
                     "X-GitHub-Api-Version": "2022-11-28",
                     "Content-Type": "application/json"},
        )
        try:
            with urlopen(request, timeout=15) as response:
                return json.load(response)
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
    def status(self, sha, state, description):
        if not SHA.fullmatch(str(sha)) or state not in ("pending", "success", "failure"):
            raise AdmissionError("unsafe status target")
        self.request("POST", "/statuses/" + sha, {
            "state": state,
            "context": STATUS_CONTEXT,
            "description": description[:140],
        })

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
            with urlopen(request, timeout=15) as response:
                metadata = json.loads(response.headers["Dropbox-API-Result"])
                if metadata.get("path_display", "").casefold() != path.casefold():
                    raise AdmissionError("Dropbox read-back path differs from reference")
                if metadata.get(".tag") != "file" or not metadata.get("id"):
                    raise AdmissionError("Dropbox record is not a downloadable file")
                content = response.read(131073)
                if len(content) > 131072:
                    raise AdmissionError("Dropbox record exceeds 128KiB bound")
                return json.loads(content.decode("utf-8"))
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

def execute(event, github, dropbox_token):
    """Publish a commit status only for the observed exact GitHub PR head."""
    number = event_pr_number(event)
    pr = github.pr(number)
    initial = candidate_snapshot(pr)
    head = (pr.get("head") or {}).get("sha")
    if not SHA.fullmatch(str(head)):
        raise AdmissionError("invalid initial PR head")
    github.status(head, "pending", "Checking external evidence and independent review")
    try:
        dropbox = Dropbox(dropbox_token) if isinstance(dropbox_token, str) else dropbox_token
        commits = github.pages(f"/pulls/{number}/commits")
        read, recheck = record_readset(dropbox.record)
        review = read(agent_review_reference(pr.get("body") or ""))
        verify_candidate(pr, review, commits, read, head, github)
        recheck()
        if candidate_snapshot(github.pr(number)) != initial:
            raise AdmissionError("PR state changed during admission (head changed or other drift)")
    except AdmissionError as error:
        github.status(head, "failure", str(error))
        raise
    github.status(head, "success", "Verified evidence and independent exact-head agent review")

def main():
    try:
        path = os.environ.get("GITHUB_EVENT_PATH")
        if not path:
            raise AdmissionError("GITHUB_EVENT_PATH not provided")
        with open(path, encoding="utf-8") as event_file:
            event = json.load(event_file)
        github = GitHub(os.environ.get("GITHUB_REPOSITORY", ""), os.environ.get("GITHUB_TOKEN", ""))
        execute(event, github, os.environ.get("FLOWSTATE_DROPBOX_METADATA_READ_TOKEN", ""))
    except (AdmissionError, ValueError, OSError) as error:
        print("FAIL:", str(error), file=sys.stderr)
        return 1
    print("PASS: exact candidate has independently reviewed Dropbox evidence")
    return 0

if __name__ == "__main__":
    sys.exit(main())
