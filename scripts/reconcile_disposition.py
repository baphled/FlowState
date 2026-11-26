"""Validate the release disposition of a work-packet record; every defect names its topic."""
import re
from scripts.reconcile_config import ReconcileError

TOPICS = ("semantic_versioning", "breaking_changes", "api_schema_compatibility", "database_migrations",
          "dependency_management", "packaging_distribution", "supported_platforms", "artefact_integrity",
          "release_qualification", "documentation_changelogs", "rollback_forward_recovery",
          "post_release_acceptance")
KINDS = ("commit", "pull_request", "workflow", "record")
MIN_STATEMENT = 80
MAX_EVIDENCE = 32
DIGEST = re.compile(r"[0-9a-f]{64}")
APPLICABLE, NOT_APPLICABLE = "applicable", "not-applicable"
SHAPES = {APPLICABLE: {"status", "evidence"}, NOT_APPLICABLE: {"status", "statement"}}


def _item_defect(item):
    """Why one evidence item is malformed, or None."""
    if not isinstance(item, dict) or set(item) != {"kind", "ref", "sha256"}:
        return "evidence items need exactly kind, ref and sha256"
    checks = (("kind", item["kind"] in KINDS), ("ref", isinstance(item["ref"], str) and bool(item["ref"].strip())),
              ("sha256", isinstance(item["sha256"], str) and bool(DIGEST.fullmatch(item["sha256"]))))
    bad = [name for name, ok in checks if not ok]
    return "malformed evidence " + ", ".join(bad) if bad else None


def _applicable_defect(topic):
    """Why an applicable topic's evidence is unusable, or None."""
    evidence = topic["evidence"]
    if not isinstance(evidence, list) or not 1 <= len(evidence) <= MAX_EVIDENCE:
        return f"needs 1 to {MAX_EVIDENCE} evidence items"
    return next(filter(None, map(_item_defect, evidence)), None)


def _not_applicable_defect(topic):
    """Why a not-applicable statement is too thin, or None."""
    statement = topic["statement"]
    if not isinstance(statement, str) or len(statement.strip()) < MIN_STATEMENT:
        return f"statement must be a string of at least {MIN_STATEMENT} characters"
    return None


def _topic_defect(topic):
    """Why one topic is malformed, or None."""
    status = topic.get("status") if isinstance(topic, dict) else None
    if not isinstance(status, str) or status not in SHAPES or set(topic) != SHAPES[status]:
        return "must be exactly {status, evidence} when applicable or {status, statement} when not-applicable"
    return (_applicable_defect if status == APPLICABLE else _not_applicable_defect)(topic)


def _is_applicable(topic):
    """True only for a well-formed applicable topic."""
    return _topic_defect(topic) is None and topic["status"] == APPLICABLE


def _topic_errors(disposition):
    """Missing, unexpected and malformed topics, each named."""
    errors = [f"{topic}: missing" for topic in TOPICS if topic not in disposition]
    errors += [f"{topic}: unexpected topic" for topic in sorted(map(str, set(disposition) - set(TOPICS)))]
    for topic in TOPICS:
        defect = _topic_defect(disposition[topic]) if topic in disposition else None
        errors += [f"{topic}: {defect}"] if defect else []
    return errors


def _sensitive_errors(disposition, allowed_paths, release_sensitive_paths):
    """A literal release-sensitive allowed path demands one applicable topic."""
    literal = {path for path in allowed_paths if "*" not in path}
    sensitive = {path for path in release_sensitive_paths if "*" not in path}
    if not literal & sensitive or any(_is_applicable(disposition.get(topic)) for topic in TOPICS):
        return []
    return ["release_sensitive_paths: a packet touching " + ", ".join(sorted(literal & sensitive))
            + " must mark at least one topic applicable"]


def disposition_errors(disposition, allowed_paths=(), release_sensitive_paths=()):
    """Return one message per defect (empty when valid); never raises for bad input shapes of `disposition`.

    `allowed_paths` and `release_sensitive_paths` are the packet's patterns and the configured list.
    """
    if not isinstance(disposition, dict):
        return ["release_disposition: must be an object with exactly the twelve topics"]
    return (_topic_errors(disposition)
            + _sensitive_errors(disposition, allowed_paths, release_sensitive_paths))


def validate_disposition(disposition, allowed_paths=(), release_sensitive_paths=()):
    """Raise ReconcileError naming every defective topic; return None when the disposition is valid."""
    errors = disposition_errors(disposition, allowed_paths, release_sensitive_paths)
    if errors:
        raise ReconcileError("release disposition rejected: " + "; ".join(errors))
