#!/usr/bin/env bash
# Rebase-merge an admitted PR so its exact commits land verbatim on the base.
# Implements rules/MERGE_ADMISSION.md 'Rebase merge trailer policy': branch
# protection requires linear history and the per-commit Project-Metadata guard
# requires exactly one trailer per landed commit, so merge-pr rebases instead
# of squashing -- each PR commit keeps its own message and trailer, and the
# reviewed head SHA is exactly the SHA that merges.
# Usage: scripts/merge_pr.sh --pr <number> [--base <branch>] [--dry-run]
set -euo pipefail

readonly ADMISSION_CONTEXT='flowstate/merge-admission'
readonly TRAILER_RE='^Project-Metadata: dropbox:/FlowState/Metadata/attempts/[^/]+/[^/]+\.json$'
readonly SUBJECT_RE='^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-zA-Z0-9_-]+\))?!?: .+'

die() { echo "merge_pr: $*" >&2; exit 1; }
usage() {
    echo 'Usage: scripts/merge_pr.sh --pr <number> [--base <branch>] [--dry-run]' >&2
    exit "${1:-0}"
}

pr='' base='' dry_run=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        --pr) [[ $# -ge 2 ]] || usage 1; pr=$2; shift 2 ;;
        --base) [[ $# -ge 2 ]] || usage 1; base=$2; shift 2 ;;
        --dry-run) dry_run=true; shift ;;
        -h|--help) usage 0 ;;
        *) echo "merge_pr: unknown argument: $1" >&2; usage 1 ;;
    esac
done
[[ $pr =~ ^[0-9]+$ ]] || usage 1
base=${base:-next}

# Step 1: the PR must exist, be OPEN, non-draft, mergeable, and target the base.
if ! pr_json=$(gh pr view "$pr" --json state,isDraft,mergeable,baseRefName,headRefOid); then
    die "PR $pr not found"
fi
field() { jq -r "$1" <<<"$pr_json"; }
[[ $(field .state) == OPEN ]] || die "PR $pr state is '$(field .state)', expected OPEN"
[[ $(field .isDraft) == false ]] || die "PR $pr is a draft"
[[ $(field .mergeable) == MERGEABLE ]] || die "PR $pr is not mergeable"
[[ $(field .baseRefName) == "$base" ]] || die "PR $pr base is '$(field .baseRefName)', expected '$base'"
head=$(field .headRefOid)

# Resolve the repository for gh api calls: GH_REPO wins, else the origin remote
# (git@github.com:OWNER/REPO.git or https://github.com/OWNER/REPO.git).
repo=${GH_REPO:-$(git remote get-url origin \
    | sed -E 's#.*github\.com[:/]##; s#\.git$##')}
[[ $repo =~ ^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$ ]] \
    || die "cannot resolve repository (set GH_REPO=OWNER/REPO); got '$repo'"

# Step 2: flowstate/merge-admission commit status must be SUCCESS on the head.
if ! status_json=$(gh api "repos/$repo/commits/$head/status"); then
    die "cannot read commit status for $head"
fi
state=$(jq -r --arg ctx "$ADMISSION_CONTEXT" \
    '[.statuses[]? | select(.context == $ctx) | .state] | first // "ABSENT"' <<<"$status_json")
state=${state^^}
[[ $state == SUCCESS ]] || die "status $ADMISSION_CONTEXT on $head is '$state', expected SUCCESS"

# Steps 3+4: the head commit must carry exactly one well-formed trailer.
# ai-commit enforces one trailer per branch commit at authoring and the
# per-commit guard re-enforces it on every landed commit; this pre-merge
# check fail-closes on the head commit that admission reviewed.
if ! commit_json=$(gh api "repos/$repo/commits/$head"); then
    die "cannot read commit $head"
fi
message=$(jq -r .commit.message <<<"$commit_json")
count=$(grep -cE '^Project-Metadata:' <<<"$message" || true)
[[ $count -eq 1 ]] || die "expected exactly 1 Project-Metadata trailer on $head, found $count"
trailer=$(grep -E '^Project-Metadata:' <<<"$message" || true)
grep -Eq "$TRAILER_RE" <<<"$trailer" || die "trailer '$trailer' does not match the dropbox attempts pattern"

# Step 5: the head commit subject lands verbatim under --rebase, so it must
# be a Conventional Commit subject. commitlint checks every branch commit on
# CI anyway; this retained local check fail-closes on the exact subject that
# becomes the new base tip.
subject=$(head -n 1 <<<"$message")
grep -Eq "$SUBJECT_RE" <<<"$subject" \
    || die "head commit subject '$subject' is not a Conventional Commit subject"

# Step 6: rebase-merge. Never squash, never inject --subject/--body: rebase
# preserves each commit's own message and trailer, so the reviewed SHA is
# exactly what lands.
if [[ $dry_run == true ]]; then
    printf 'gh pr merge %q --rebase\n' "$pr"
    exit 0
fi
gh pr merge "$pr" --rebase
