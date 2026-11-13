#!/usr/bin/env python3
"""Fail-closed review finding handoff for a single exact FlowState PR candidate.

This script reads GitHub PR comments/reviews (including inline comments) and a
Dropbox-exported immutable review ledger. No GitHub review state is treated as
an independent verifier identity, and no PASS is inferred from green CI.

Usage:
    python3 -B scripts/review_handoff.py --repo baphled/FlowState --pr 260 \
        --head <full-sha> --metadata /tmp/review-findings.json

The coordinator supplies a Dropbox read-back copy at --metadata. The script
never writes task evidence to the checkout or to the Dev documentation pack.
"""

import argparse
import json
import os
import re
import sys
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

SHA_RE = re.compile(r"^[0-9a-f]{40}$")
ID_RE = re.compile(r"\bF\d+-\d{2,}\b")
REPO_RE = re.compile(r"^[\w.-]+/[\w.-]+$")
BLOCKING = frozenset(("open", "changes_required", "reopened"))
CLOSED = frozenset(("fixed", "rejected", "deferred", "accepted"))


class HandoffError(RuntimeError):
    """Fail-closed input, network or ledger validation failure."""


def github_get(path, token):
    """Read GitHub REST JSON, including all paginated entries."""
    if not token:
        raise HandoffError("GITHUB_TOKEN is required for verified PR retrieval")
    results = []
    page = 1
    while True:
        req = Request(
            "https://api.github.com" + path + ("&" if "?" in path else "?")
            + "per_page=100&page=" + str(page),
            headers={"Authorization": "Bearer " + token,
                     "Accept": "application/vnd.github+json",
                     "X-GitHub-Api-Version": "2022-11-28"},
        )
        try:
            with urlopen(req, timeout=20) as resp:
                result = json.load(resp)
        except (HTTPError, URLError, ValueError, TimeoutError) as exc:
            raise HandoffError("GitHub retrieval failed: " + str(exc)) from exc
        if isinstance(result, dict):
            if page != 1:
                raise HandoffError("Unexpected paginated GitHub object")
            return result
        if not isinstance(result, list):
            raise HandoffError("Unexpected GitHub response type")
        results.extend(result)
        if len(result) < 100:
            return results
        page += 1
        if page > 100:
            raise HandoffError("GitHub pagination exceeded safe limit")


def parse_ledger(raw, pr, head):
    """Check ledger identity and retain explicit, stable finding dispositions."""
    if not isinstance(raw, dict) or raw.get("pr") != pr:
        raise HandoffError("Dropbox review ledger PR does not match")
    if raw.get("head_sha") != head:
        raise HandoffError("Dropbox review ledger head is stale; requalify")
    findings = raw.get("findings")
    if not isinstance(findings, list):
        raise HandoffError("Dropbox review ledger must contain findings")
    output = {}
    for item in findings:
        if not isinstance(item, dict):
            raise HandoffError("Malformed review finding")
        fid, state = item.get("id"), item.get("status")
        if not isinstance(fid, str) or not ID_RE.fullmatch(fid):
            raise HandoffError("Invalid stable finding ID")
        if state not in BLOCKING | CLOSED:
            raise HandoffError("Invalid finding state: " + str(state))
        if fid in output:
            raise HandoffError("Duplicate finding ID: " + fid)
        if state in CLOSED and not item.get("evidence"):
            raise HandoffError("Resolved finding lacks correction/review evidence: " + fid)
        output[fid] = item
    return output


def evidence_links(comment):
    """Return only explicit finding IDs, never infer disposition from prose."""
    return set(ID_RE.findall(comment.get("body") or ""))


def evaluate(pr_data, comments, reviews, inline, ledger, head):
    """Produce an implementer handoff with no inferred review acceptance."""
    if pr_data.get("head", {}).get("sha") != head:
        raise HandoffError("PR changed while retrieving evidence; retry exact head")
    if pr_data.get("state") != "open":
        raise HandoffError("Candidate is not an open PR")
    if not all(isinstance(items, list) for items in (comments, reviews, inline)):
        raise HandoffError("Incomplete GitHub review surfaces")
    actionable = {}
    sources = (("conversation", comments), ("review", reviews), ("inline", inline))
    for kind, records in sources:
        for item in records:
            if not isinstance(item, dict):
                raise HandoffError("Malformed GitHub review data")
            for fid in evidence_links(item):
                actionable.setdefault(fid, []).append({
                    "surface": kind,
                    "url": item.get("html_url") or "",
                    "body": item.get("body") or "",
                    "path": item.get("path"),
                    "line": item.get("line"),
                })
    missing = sorted(set(actionable) - set(ledger))
    if missing:
        raise HandoffError("GitHub findings missing from Dropbox ledger: " + ", ".join(missing))
    findings = []
    for fid, finding in sorted(ledger.items()):
        if finding["status"] in BLOCKING:
            findings.append({"id": fid, "status": finding["status"],
                             "description": finding.get("description", ""),
                             "requested_change": finding.get("requested_change", ""),
                             "test_oracle": finding.get("test_oracle", ""),
                             "links": actionable.get(fid, [])})
    # Reviews with no structured finding ID can still be blocking: fail closed.
    unstructured_requests = [r.get("html_url") or "unknown" for r in reviews
                             if r.get("state", "").upper() == "CHANGES_REQUESTED"
                             and not evidence_links(r)]
    if unstructured_requests:
        raise HandoffError("Unstructured change requests require triage: "
                           + ", ".join(unstructured_requests))
    return {"pr": pr_data["number"], "head_sha": head,
            "status": "changes_required" if findings else "review_pending",
            "findings": findings,
            "review_accepted": False}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", required=True)
    parser.add_argument("--pr", type=int, required=True)
    parser.add_argument("--head", required=True)
    parser.add_argument("--metadata", required=True,
                        help="locally materialised immutable Dropbox ledger")
    args = parser.parse_args(argv)
    try:
        if not REPO_RE.fullmatch(args.repo) or args.pr < 1 or not SHA_RE.fullmatch(args.head):
            raise HandoffError("Invalid repo, PR number or full commit SHA")
        with open(args.metadata, encoding="utf-8") as file:
            ledger = parse_ledger(json.load(file), args.pr, args.head)
        api = "/repos/{}/".format(args.repo)
        token = os.environ.get("GITHUB_TOKEN")
        pr_data = github_get(api + "pulls/" + str(args.pr), token)
        comments = github_get(api + "issues/" + str(args.pr) + "/comments", token)
        reviews = github_get(api + "pulls/" + str(args.pr) + "/reviews", token)
        inline = github_get(api + "pulls/" + str(args.pr) + "/comments", token)
        report = evaluate(pr_data, comments, reviews, inline, ledger, args.head)
        print(json.dumps(report, indent=2, sort_keys=True))
        return 2 if report["findings"] else 0
    except (OSError, ValueError, HandoffError) as exc:
        print("Review handoff blocked: " + str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
