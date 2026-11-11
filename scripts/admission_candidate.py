"""Bind PR state and commit manifests to exact candidate data."""
import hashlib
import json
from scripts.admission_metadata import AdmissionError, SHA, metadata_reference, validate_record
from scripts.admission_review import agent_review_reference, validate_agent_review
from scripts.admission_checks import check_runs_page, verify_required_checks

def candidate_snapshot(pr):
    """Capture admission-sensitive PR fields as immutable values."""
    return tuple(json.dumps(pr.get(key), sort_keys=True, allow_nan=False)
                 for key in ("number", "head", "base", "state", "draft", "body", "user"))

def validate_candidate_state(pr, commits, expected_sha):
    head_sha = ((pr.get("head") or {}).get("sha"))
    author = ((pr.get("user") or {}).get("login"))
    if pr.get("state") != "open" or pr.get("draft") or (pr.get("base") or {}).get("ref") != "next":
        raise AdmissionError("not an open, ready PR targeting next")
    if not author or not SHA.fullmatch(str(head_sha)) or head_sha != expected_sha:
        raise AdmissionError("candidate SHA or author cannot be verified")
    if not SHA.fullmatch(str((pr.get("base") or {}).get("sha"))):
        raise AdmissionError("candidate base SHA cannot be verified")
    if not commits or len(commits) > 1000 or commits[-1].get("sha") != head_sha:
        raise AdmissionError("incomplete or changed PR commit list")
    return head_sha

def verify_candidate(pr, review, commits, fetch_record, expected_sha, github):
    """Recheck every commit and reject mismatched external evidence."""
    head_sha = validate_candidate_state(pr, commits, expected_sha)
    previous = pr["base"]["sha"]
    for commit in commits:
        sha = commit.get("sha")
        parents = commit.get("parents") or []
        if not SHA.fullmatch(str(sha)) or not parents or not SHA.fullmatch(str(parents[0].get("sha"))):
            raise AdmissionError("commit SHA/parent unavailable")
        if len(parents) != 1 or parents[0]["sha"] != previous:
            raise AdmissionError("candidate commits do not form a linear chain from current base")
        previous = sha
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

