"""Verify required CI checks succeeded on the exact candidate head."""
from scripts.admission_metadata import AdmissionError, SHA

MAX_CHECK_PAGES = 10


def required_check_names(required):
    """Validate trusted configuration; never read required checks from PR data."""
    if not required or not isinstance(required, (tuple, list)):
        raise AdmissionError("required checks configuration missing or invalid")
    names = list(required)
    for name in names:
        if not isinstance(name, str) or not name or name.strip() != name:
            raise AdmissionError("required checks configuration missing or invalid")
    return names


def check_runs_page(request, sha, page):
    """Fetch one check-runs page through the read-only GitHub request adapter."""
    if not SHA.fullmatch(str(sha)) or not isinstance(page, int) or page < 1:
        raise AdmissionError("invalid check-runs request")
    record = request("GET", f"/commits/{sha}/check-runs?per_page=100&page={page}")
    runs = record.get("check_runs")
    if not isinstance(runs, list):
        raise AdmissionError("invalid GitHub check-runs response")
    return runs


def classify_run(run, pr_head):
    """Reduce one check run to a verdict string; success requires exact head."""
    if run.get("status") != "completed":
        return "incomplete"
    conclusion = run.get("conclusion")
    if conclusion == "success" and run.get("head_sha") != pr_head:
        return "different head"
    return conclusion


def collect_check_verdicts(github, pr_head, names):
    """Collect the first verdict per required check name across pages."""
    observed = {}
    wanted = set(names)
    for page in range(1, MAX_CHECK_PAGES + 1):
        runs = github.check_runs(pr_head, page)
        if not isinstance(runs, list):
            raise AdmissionError("invalid GitHub check-runs response")
        for run in runs:
            name = run.get("name")
            if name in wanted and name not in observed:
                observed[name] = classify_run(run, pr_head)
        if len(runs) < 100:
            return observed
    raise AdmissionError("GitHub check-runs exceed page bound")


def verify_required_checks(pr_head, github, required):
    """Fail closed unless every required check is green on this exact head SHA."""
    names = required_check_names(required)
    observed = collect_check_verdicts(github, pr_head, names)
    for name in names:
        verdict = observed.get(name, "missing")
        if verdict != "success":
            raise AdmissionError(
                f"required check {name} did not succeed on the candidate head: {verdict}")
    return True
