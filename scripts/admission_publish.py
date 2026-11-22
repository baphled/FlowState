"""Publish admission statuses without noise from heads that are not admitted; never dedupe a success."""
import time

from scripts.admission_budget import EXHAUSTED, STATUS_BUDGET, STATUS_LIMIT, UNCOUNTED, read_spent
from scripts.admission_metadata import IN_PROGRESS, WAITING_CHECK

# A record older than this is treated as absent, so the publisher re-asserts its status.
RECORD_TTL = 600
# A required check that has not reported after this long is published as a failure, not as waiting.
CHECK_DEADLINE = 3600
# A head's status count is read from GitHub at most once per this many seconds (scan cycles are 15 s apart).
READ_INTERVAL = 10


class PublishedRecords(dict):
    """The process-lived record, head -> (state, description, time), plus per-head state that must
    outlive record expiry: `counts` (statuses GitHub holds, plus those published since), `final`
    (heads that take no further status), `waiting` (head -> (check, first time seen)) and `reads`
    (head -> time of the last count read, successful or not)."""
    def __init__(self):
        super().__init__()
        self.counts, self.final, self.waiting, self.reads = {}, {}, {}, {}


def retain_heads(last, candidates):
    """Bound the record: forget every head that is not the head of a listed candidate PR."""
    heads = {(pr.get('head') or {}).get('sha') for pr in candidates}
    for table in (last, last.counts, last.final, last.waiting, last.reads):
        for head in [head for head in table if head not in heads]:
            del table[head]


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

    Two bounds sit on top, both kept in `last` so that record expiry cannot reset them. A head
    holds at most STATUS_BUDGET statuses: the publication that would cross it becomes one final
    failure, and once THAT IS PUBLISHED the head is terminal (nothing more, never a success)
    until its SHA changes. A publication that raised is never counted from memory: the count is
    read from GitHub again, so an outage cannot end a head whose success still stands. While the
    count cannot be read, every status becomes a failure saying so and the head is not terminal.
    The count is read at most once per READ_INTERVAL per head, whether the read succeeded or not; a
    call inside that window with no held count treats it as unknown (the failure above), never as a
    remembered number, so a stale or unknown count cannot publish a success.
    Waiting for the same required check for CHECK_DEADLINE becomes a failure; the review wait
    has no deadline.
    """
    def __init__(self, issuer, last, reader, clock=time.monotonic):
        self.issuer, self.reader, self.clock = issuer, reader, clock
        self.last = PublishedRecords() if last is None else last

    def record(self, sha):
        """Return the unexpired (state, description, time) record for the head, or None."""
        record = self.last.get(sha)
        if record is not None and self.clock() - record[2] >= RECORD_TTL:
            return None
        return record

    def deadline(self, sha, state, description):
        """Turn waiting for the SAME required check for CHECK_DEADLINE into a failure; no other state ages."""
        if (state, description) == ('pending', IN_PROGRESS):
            return state, description
        if state != 'pending' or not description.startswith(WAITING_CHECK):
            self.last.waiting.pop(sha, None)
            return state, description
        name = description[len(WAITING_CHECK):]
        seen = self.last.waiting.get(sha)
        if seen is None or seen[0] != name:
            seen = self.last.waiting[sha] = (name, self.clock())
        if self.clock() - seen[1] >= CHECK_DEADLINE:
            return 'failure', f'required check {name} has not reported after {CHECK_DEADLINE // 60} minutes'
        return state, description

    def spent(self, sha):
        """Statuses on the head, read from GitHub on first sight and again after any publication
        that raised (the held count is dropped before each one); None while it cannot be read, and
        None without a read while the last read of this head is under READ_INTERVAL old."""
        if sha not in self.last.counts:
            read = self.last.reads.get(sha)
            if read is not None and 0 <= self.clock() - read < READ_INTERVAL:
                return None
            self.last.reads[sha] = self.clock()
            try:
                count, done = read_spent(self.reader, sha)
            except Exception:
                return None
            self.last.counts[sha] = count
            if done or count >= STATUS_LIMIT:
                self.last.final[sha] = True
        return self.last.counts[sha]

    def status(self, sha, state, description):
        if self.last.final.get(sha):
            return
        state, description = self.deadline(sha, state, description)
        spent = self.spent(sha)
        if self.last.final.get(sha):
            return
        if spent is None:
            state, description = 'failure', UNCOUNTED
        elif spent >= STATUS_BUDGET:
            state, description = 'failure', EXHAUSTED
        record = self.record(sha)
        if state == 'pending' and description == IN_PROGRESS:
            if record is not None and record[0] != 'success':
                return
        elif state != 'success' and record is not None and record[:2] == (state, description):
            return
        self.last.pop(sha, None)
        self.last.counts.pop(sha, None)
        self.issuer.status(sha, state, description)
        if spent is not None:
            self.last.counts[sha] = spent + 1
        if description == EXHAUSTED:
            self.last.final[sha] = True
        self.last[sha] = (state, description, self.clock())
