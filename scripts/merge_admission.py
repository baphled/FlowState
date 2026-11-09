#!/usr/bin/env python3
"""Fail-closed, trusted PR admission check against GitHub and Dropbox.
This program must run from the default/protected branch in a privileged runner,
NEVER from the pull request checkout. A status is posted on the exact PR HEAD;
branch protection must require that status and the ordinary CI Build context.
"""
import base64
import binascii
import hashlib
import json
import os
import re
import sys
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen
STATUS_CONTEXT = "flowstate/merge-admission"
REFERENCE = re.compile(
    r"dropbox:/FlowState/Metadata/attempts/"
    r"[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*\.json"
)
SHA = re.compile(r"[0-9a-f]{40}")
DIGEST = re.compile(r"[0-9a-f]{64}")
REQUIRED_READS = {
    "/FlowState/Dev/00-AGENT-READ-FIRST.md",
    "/FlowState/Dev/AGENTS.md",
    "/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md",
    "/FlowState/Metadata/ACTIVE.json",
}

class AdmissionError(Exception): pass
def metadata_reference(message):
    """Extract the single neutral trailer, refusing duplicates or malformed paths."""
    refs = re.findall(r"^Project-Metadata: (.+)$", message, flags=re.M)
    if len(refs) != 1 or not REFERENCE.fullmatch(refs[0]):
        raise AdmissionError("one valid Project-Metadata reference required per commit")
    return refs[0]

def agent_review_reference(body):
    refs = re.findall(r"^Agent-Review: (.+)$", body if isinstance(body, str) else "", flags=re.M)
    if len(refs) != 1 or not REFERENCE.fullmatch(refs[0]):
        raise AdmissionError("one external agent-review record required in PR description")
    return refs[0]

def validate_record(record, reference, parent_sha):
    """Validate externally downloaded record against the exact commit parent."""
    if not isinstance(record, dict):
        raise AdmissionError("Dropbox record must be a JSON object")
    if record.get("record_type") != "flowstate-project-metadata-v1":
        raise AdmissionError("unsupported Dropbox metadata record")
    if record.get("metadata_ref") != reference:
        raise AdmissionError("Dropbox metadata reference mismatch")
    if record.get("base_sha") != parent_sha:
        raise AdmissionError("Dropbox metadata base SHA does not match commit parent")
    if not DIGEST.fullmatch(str(record.get("docs_pack_manifest_sha256", ""))):
        raise AdmissionError("invalid documentation pack digest")
    attribution = record.get("attribution")
    if not isinstance(attribution, dict) or any(
        not isinstance(attribution.get(k), str) or not attribution[k].strip()
        for k in ("agent", "provider", "model", "harness", "source")
    ):
        raise AdmissionError("missing observed/unverified attribution and source")
    reads = record.get("dropbox_reads")
    if not isinstance(reads, list):
        raise AdmissionError("missing Dropbox read observations")
    observed = set()
    for item in reads:
        if not isinstance(item, dict):
            raise AdmissionError("invalid Dropbox read observation")
        path = str(item.get("path", ""))
        path = re.sub(r"^ns:[0-9]+//", "/", path)
        if not DIGEST.fullmatch(str(item.get("sha256", ""))) or not item.get("file_id"):
            raise AdmissionError("unverified Dropbox read observation")
        observed.add(path)
    if not REQUIRED_READS <= observed:
        raise AdmissionError("missing required current Dropbox source observations")
    manifest = (record.get("local_candidate") or {}).get("paths")
    if not isinstance(manifest, dict) or not manifest:
        raise AdmissionError("missing candidate source manifest")
    return manifest

def validate_agent_review(review, reference, head_sha, number):
    """Verify substantive, separate-run agent review of the exact candidate."""
    if not isinstance(review, dict) or review.get("record_type") != "flowstate-agent-review-v1":
        raise AdmissionError("missing or invalid independent agent review")
    if review.get("metadata_ref") != reference or review.get("head_sha") != head_sha:
        raise AdmissionError("agent review does not bind the exact candidate")
    if review.get("pr_number") != number or review.get("verdict") != "PASS":
        raise AdmissionError("agent review is not an accepted verdict for this PR")
    reviewer, implementer = review.get("reviewer"), review.get("implementer")
    if not isinstance(reviewer, dict) or not isinstance(implementer, dict):
        raise AdmissionError("missing agent run identities")
    reviewer_run, implementer_run = reviewer.get("run_id"), implementer.get("run_id")
    if (reviewer.get("role") != "verifier" or
        not isinstance(reviewer_run, str) or not reviewer_run.strip() or
        not isinstance(implementer_run, str) or not implementer_run.strip() or
        reviewer_run == implementer_run or
        not reviewer.get("identity_evidence") or not implementer.get("identity_evidence")):
        raise AdmissionError("agent review does not establish separate observed runs")
    oracle = review.get("negative_oracle")
    if (not isinstance(oracle, dict) or oracle.get("result") != "PASS" or
        not isinstance(oracle.get("evidence"), str) or not oracle["evidence"].strip()):
        raise AdmissionError("independent negative-oracle evidence required")
    if not isinstance(review.get("findings"), list) or not str(review.get("summary") or "").strip():
        raise AdmissionError("substantive agent findings and summary required")

def verify_candidate(pr, review, commits, fetch_record, expected_sha, github):
    """Validate a single live candidate and each of its commit evidence records."""
    head_sha = ((pr.get("head") or {}).get("sha"))
    author = ((pr.get("user") or {}).get("login"))
    if pr.get("state") != "open" or pr.get("draft") or (pr.get("base") or {}).get("ref") != "next":
        raise AdmissionError("not an open, ready PR targeting next")
    if not author or not SHA.fullmatch(str(head_sha)) or head_sha != expected_sha:
        raise AdmissionError("candidate SHA or author cannot be verified")
    if not commits or len(commits) > 1000 or commits[-1].get("sha") != head_sha:
        raise AdmissionError("incomplete or changed PR commit list")
    for commit in commits:
        sha = commit.get("sha")
        parents = commit.get("parents") or []
        if not SHA.fullmatch(str(sha)) or not parents or not SHA.fullmatch(str(parents[0].get("sha"))):
            raise AdmissionError("commit SHA/parent unavailable")
        reference = metadata_reference((commit.get("commit") or {}).get("message", ""))
        manifest = validate_record(fetch_record(reference), reference, parents[0]["sha"])
        verify_files(github, sha, manifest)
    validate_agent_review(review, agent_review_reference(pr.get("body") or ""), head_sha, pr.get("number"))
    return head_sha

def verify_files(github, sha, manifest):
    """Bind Dropbox manifest to the actual complete GitHub commit diff."""
    files = github.commit_files(sha)
    if len(files) != len(manifest) or {item.get('filename') for item in files} != set(manifest):
        raise AdmissionError('candidate changed-path set differs from Dropbox evidence')
    for item in files:
        if item.get('status') not in ('added', 'modified') or item.get('previous_filename'):
            raise AdmissionError('unattested deletion or rename in candidate')
        props = manifest[item['filename']]
        blob_sha = item.get('sha')
        if not isinstance(props, dict) or not SHA.fullmatch(str(blob_sha)):
            raise AdmissionError('invalid candidate blob evidence')
        actual = github.blob(blob_sha)
        if hashlib.sha256(actual).hexdigest() != props.get('sha256') or len(actual) != props.get('bytes'):
            raise AdmissionError('candidate blob does not match Dropbox manifest')

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

def event_pr_number(event):
    """Use the PR number from trusted GitHub event payload, never an arbitrary URL."""
    number = (event.get("pull_request") or {}).get("number")
    if number is None and (event.get("issue") or {}).get("pull_request"):
        number = event["issue"].get("number")
    if number is None:
        number = (event.get("inputs") or {}).get("pull_request")
        if isinstance(number, str) and number.isascii() and number.isdecimal():
            number = int(number)
    if not isinstance(number, int) or isinstance(number, bool) or number <= 0:
        raise AdmissionError("event has no valid PR number")
    return number

def admission_pr_state(pr):
    """Capture every mutable PR field used to grant admission."""
    head, base = pr.get("head") or {}, pr.get("base") or {}
    return (
        head.get("sha"), base.get("ref"), base.get("sha"),
        pr.get("state"), pr.get("draft"), pr.get("body"),
    )


def execute(event, github, dropbox_token):
    """Publish a commit status only for the observed exact GitHub PR head."""
    number = event_pr_number(event)
    pr = github.pr(number)
    head = (pr.get("head") or {}).get("sha")
    if not SHA.fullmatch(str(head)):
        raise AdmissionError("invalid initial PR head")
    github.status(head, "pending", "Checking external evidence and independent review")
    try:
        dropbox = Dropbox(dropbox_token) if isinstance(dropbox_token, str) else dropbox_token
        commits = github.pages(f"/pulls/{number}/commits")
        review = dropbox.record(agent_review_reference(pr.get("body") or ""))
        verify_candidate(pr, review, commits, dropbox.record, head, github)
        if admission_pr_state(github.pr(number)) != admission_pr_state(pr):
            raise AdmissionError("PR candidate changed during admission")
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
