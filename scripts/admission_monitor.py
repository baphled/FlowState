"""Bounded, read-only monitor that the trusted publisher has reported for a candidate head."""
import time

from scripts.admission_metadata import AdmissionError, SHA
from scripts.status_issuer import STATUS_CONTEXT


def qualified_status(reader, head):
    """Read creator-bearing status records for the exact candidate SHA."""
    response = reader.request('GET', f'/statuses/{head}?per_page=100')
    if not isinstance(response, list):
        raise AdmissionError('publisher status records unavailable for the candidate')
    matches = [s for s in response if s.get('context') == STATUS_CONTEXT]
    status = next(iter(matches), {})
    if status and status.get('creator', {}).get('login') != 'flowstate-merge-admission[bot]':
        raise AdmissionError('admission status came from the wrong publisher')
    return status


def watch(reader, head, timeout=120, clock=time.monotonic, sleep=time.sleep):
    """Monitor publisher liveness for the exact candidate SHA; return its status.

    Any `flowstate/merge-admission` status from the trusted publisher (success,
    pending, failure or error) proves the publisher is running, so it is returned
    and the job passes: the required status context, not this monitor, decides the
    merge. A status from any other creator raises at once, and so does silence
    until the deadline, which means the publisher is probably not running.
    """
    if not SHA.fullmatch(str(head)):
        raise AdmissionError('monitor requires the exact candidate SHA')
    deadline = clock() + timeout
    while True:
        status = qualified_status(reader, head)
        if status:
            return status
        if clock() >= deadline:
            raise AdmissionError('the admission publisher has not reported for commit ' + head +
                                 ' and is probably not running; check '
                                 '`systemctl --user status flowstate-admission.service`')
        sleep(5)


def report(status):
    """Describe a publisher status, stating that the required context decides the merge.

    The description is echoed into the job log and step summary, so it is reduced to
    one bounded line of plain text first: no line can start a workflow command.
    """
    described = ' '.join(str(status.get('description')).split())[:200].replace('::', ': :')
    return ('Admission publisher has reported: flowstate/merge-admission is ' + str(status.get('state')) +
            ' (' + described + ') as of ' + str(status.get('created_at')) + '. The required status '
            'context, not this job, decides the merge.\n')
