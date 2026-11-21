"""Publish admission statuses without noise from heads that are not admitted; never dedupe a success."""
import time

from scripts.admission_metadata import IN_PROGRESS

# A record older than this is treated as absent, so the publisher re-asserts its status.
RECORD_TTL = 600


def retain_heads(last, candidates):
    """Bound the record: forget every head that is not the head of a listed candidate PR."""
    heads = {(pr.get('head') or {}).get('sha') for pr in candidates}
    for head in [head for head in (last or {}) if head not in heads]:
        del last[head]


class ChangedStatusPublisher:
    """Wrap a status issuer; `last` maps head -> (state, description, time) of the last publication.

    The record mirrors the newest status this process put on the head, whichever PR asked.
    A `success` is never deduplicated and, because the record then holds `success`, every
    evaluation begins by withdrawing it with the in-progress marker, exactly as on the base
    branch. Only a head whose record is a non-success verdict is quiet: its marker is skipped
    and an unchanged verdict is not repeated. The record is invalidated before every
    publication and written only after it returned, so any exception (even one raised after
    GitHub applied the POST, or KeyboardInterrupt) leaves no record and the next cycle
    publishes unconditionally. A record older than RECORD_TTL counts as absent.
    """
    def __init__(self, issuer, last, clock=time.monotonic):
        self.issuer, self.last, self.clock = issuer, {} if last is None else last, clock

    def record(self, sha):
        """Return the unexpired (state, description, time) record for the head, or None."""
        record = self.last.get(sha)
        if record is not None and self.clock() - record[2] >= RECORD_TTL:
            return None
        return record

    def status(self, sha, state, description):
        record = self.record(sha)
        if state == 'pending' and description == IN_PROGRESS:
            if record is not None and record[0] != 'success':
                return
        elif state != 'success' and record is not None and record[:2] == (state, description):
            return
        self.last.pop(sha, None)
        self.issuer.status(sha, state, description)
        self.last[sha] = (state, description, self.clock())
