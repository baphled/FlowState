"""ChangedStatusPublisher: a success is never deduplicated; only non-admitted heads go quiet."""
import unittest

from scripts.admission_metadata import AdmissionError, IN_PROGRESS
from scripts.admission_publish import RECORD_TTL, ChangedStatusPublisher, retain_heads

HEAD, OTHER = '2' * 40, '3' * 40


class FakeIssuer:
    """Records a POST, then optionally raises: GitHub applied it but the caller saw an error."""
    def __init__(self):
        self.posted, self.errors = [], {}

    def status(self, sha, state, description):
        self.posted.append((sha, state, description))
        if self.errors.get((state, description)):
            raise self.errors[(state, description)]


class Clock:
    def __init__(self):
        self.now = 1000.0

    def __call__(self):
        return self.now


class ChangedStatuses(unittest.TestCase):
    def setUp(self):
        self.issuer, self.last, self.clock = FakeIssuer(), {}, Clock()
        self.publisher = ChangedStatusPublisher(self.issuer, self.last, self.clock)

    def states(self):
        return [state for _, state, _ in self.issuer.posted]

    def test_first_in_progress_marker_is_published(self):
        self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        self.assertEqual(self.issuer.posted, [(HEAD, 'pending', IN_PROGRESS)])

    def test_marker_is_skipped_while_the_record_is_a_failure(self):
        self.publisher.status(HEAD, 'failure', 'bad')
        self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        self.assertEqual(self.issuer.posted, [(HEAD, 'failure', 'bad')])

    def test_marker_is_skipped_while_the_record_is_a_waiting_pending(self):
        self.publisher.status(HEAD, 'pending', 'Waiting for required check Build')
        self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        self.assertEqual(self.states(), ['pending'])

    def test_marker_is_published_when_the_record_is_a_success(self):
        self.publisher.status(HEAD, 'success', 'ok')
        self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        self.assertEqual(self.states(), ['success', 'pending'])

    def test_marker_is_published_for_another_head(self):
        self.publisher.status(HEAD, 'failure', 'bad')
        self.publisher.status(OTHER, 'pending', IN_PROGRESS)
        self.assertEqual(self.issuer.posted[-1], (OTHER, 'pending', IN_PROGRESS))

    def test_unchanged_non_success_verdict_is_not_republished(self):
        for state, text in (('failure', 'bad'), ('pending', 'Waiting for required check Build')):
            with self.subTest(state=state):
                self.publisher.status(HEAD, state, text)
                self.publisher.status(HEAD, state, text)
                self.assertEqual(self.issuer.posted.count((HEAD, state, text)), 1)

    def test_changed_description_or_state_is_published(self):
        for state, text in (('failure', 'a'), ('failure', 'b'), ('pending', 'b'), ('success', 'b')):
            self.publisher.status(HEAD, state, text)
        self.assertEqual(len(self.issuer.posted), 4)

    def test_an_unchanged_success_is_published_again(self):
        self.publisher.status(HEAD, 'success', 'ok')
        self.publisher.status(HEAD, 'success', 'ok')
        self.assertEqual(self.states(), ['success', 'success'])

    def test_record_is_written_after_the_publication_with_its_time(self):
        self.publisher.status(HEAD, 'failure', 'bad')
        self.assertEqual(self.last, {HEAD: ('failure', 'bad', 1000.0)})

    def test_record_is_gone_while_the_publication_is_in_flight(self):
        seen = []
        self.last[HEAD] = ('failure', 'bad', 1000.0)
        self.issuer.status = lambda *args: seen.append(dict(self.last))
        self.publisher.status(HEAD, 'success', 'ok')
        self.assertEqual(seen, [{}])

    def test_a_raising_publication_leaves_no_record(self):
        self.last[HEAD] = ('failure', 'bad', 1000.0)
        self.issuer.errors[('success', 'ok')] = AdmissionError('timeout after the POST landed')
        with self.assertRaises(AdmissionError):
            self.publisher.status(HEAD, 'success', 'ok')
        self.assertEqual(self.last, {})

    def test_an_interrupt_during_publication_leaves_no_record(self):
        self.last[HEAD] = ('failure', 'bad', 1000.0)
        self.issuer.errors[('success', 'ok')] = KeyboardInterrupt()
        with self.assertRaises(KeyboardInterrupt):
            self.publisher.status(HEAD, 'success', 'ok')
        self.assertEqual(self.last, {})

    def test_failed_publication_is_retried(self):
        self.issuer.errors[('failure', 'bad')] = AdmissionError('GitHub down')
        with self.assertRaises(AdmissionError):
            self.publisher.status(HEAD, 'failure', 'bad')
        self.issuer.errors.clear()
        self.publisher.status(HEAD, 'failure', 'bad')
        self.assertEqual(self.states(), ['failure', 'failure'])

    def cycle(self, success_applied_then_raises=True):
        """One cycle as run_candidate drives it: marker, success, then the revoking failure."""
        if success_applied_then_raises:
            self.issuer.errors[('success', 'ok')] = AdmissionError('creator mismatch after the POST')
        self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        try:
            self.publisher.status(HEAD, 'success', 'ok')
        except AdmissionError:
            self.publisher.status(HEAD, 'failure', 'F')

    def test_applied_then_raising_success_is_always_followed_by_a_failure(self):
        for _ in range(3):
            self.cycle()
            self.assertEqual(self.states()[-1], 'failure')

    def test_failure_is_published_after_a_success_that_was_applied_and_a_failure_that_raised(self):
        self.publisher.status(HEAD, 'failure', 'F')
        self.issuer.errors.update({('success', 'ok'): AdmissionError('late'), ('failure', 'F'): AdmissionError('blip')})
        self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        for step in (('success', 'ok'), ('failure', 'F')):
            with self.assertRaises(AdmissionError):
                self.publisher.status(HEAD, *step)
        self.issuer.errors.clear()
        self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        self.publisher.status(HEAD, 'failure', 'F')
        self.assertEqual(self.issuer.posted[-2:], [(HEAD, 'pending', IN_PROGRESS), (HEAD, 'failure', 'F')])

    def test_a_fresh_record_suppresses_the_unchanged_failure(self):
        self.publisher.status(HEAD, 'failure', 'bad')
        self.clock.now += RECORD_TTL - 1
        self.publisher.status(HEAD, 'failure', 'bad')
        self.assertEqual(len(self.issuer.posted), 1)

    def test_an_expired_record_is_republished(self):
        self.publisher.status(HEAD, 'failure', 'bad')
        self.clock.now += RECORD_TTL
        self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        self.publisher.status(HEAD, 'failure', 'bad')
        self.assertEqual(self.states(), ['failure', 'pending', 'failure'])

    def test_retain_heads_drops_heads_that_are_no_longer_candidates(self):
        self.last.update({HEAD: ('failure', 'x', 1.0), OTHER: ('success', 'y', 1.0)})
        retain_heads(self.last, [{'head': {'sha': HEAD}}, {'number': 4}])
        self.assertEqual(list(self.last), [HEAD])
