"""ChangedStatusPublisher: a success is never deduplicated; only non-admitted heads go quiet."""
import unittest

from scripts.admission_metadata import AdmissionError, IN_PROGRESS
from scripts.admission_budget import BOT_LOGIN, EXHAUSTED, STATUS_BUDGET, STATUS_LIMIT, UNCOUNTED
from scripts.admission_publish import CHECK_DEADLINE, RECORD_TTL, ChangedStatusPublisher, PublishedRecords, retain_heads
from scripts.status_issuer import STATUS_CONTEXT

HEAD, OTHER = '2' * 40, '3' * 40


class FakeIssuer:
    """Records a POST, then optionally raises: GitHub applied it but the caller saw an error."""
    def __init__(self):
        self.posted, self.errors = [], {}

    def status(self, sha, state, description):
        self.posted.append((sha, state, description))
        if self.errors.get((state, description)):
            raise self.errors[(state, description)]


def spent(count, newest='success', description='ok', context=STATUS_CONTEXT):
    """GitHub's listing, newest first: `count` statuses of the context, the newest as given."""
    mine = [{'context': context, 'state': 'success', 'description': 'ok', 'creator': {'login': BOT_LOGIN}}] * count
    if mine:
        mine[0] = dict(mine[0], state=newest, description=description)
    return mine


class FakeReader:
    """Serves `statuses` in pages of the requested size; a non-None `error` fails every request."""
    def __init__(self, statuses=(), error=None):
        self.statuses, self.error, self.paths = list(statuses), error, []

    def request(self, method, path):
        self.paths.append(path)
        if self.error:
            raise self.error
        page = int(path.rsplit('page=', 1)[1])
        return self.statuses[(page - 1) * 100:page * 100]


class Clock:
    def __init__(self):
        self.now = 1000.0

    def __call__(self):
        return self.now


class Fixture:
    def setUp(self):
        self.issuer, self.last, self.clock = FakeIssuer(), PublishedRecords(), Clock()
        self.reader = FakeReader()
        self.publisher = ChangedStatusPublisher(self.issuer, self.last, self.reader, self.clock)

    def restarted(self, reader):
        """A fresh process: empty state, the given GitHub listing."""
        self.issuer, self.last, self.reader = FakeIssuer(), PublishedRecords(), reader
        self.publisher = ChangedStatusPublisher(self.issuer, self.last, reader, self.clock)

    def states(self):
        return [state for _, state, _ in self.issuer.posted]


class ChangedStatuses(Fixture, unittest.TestCase):

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
        self.last.counts.update({HEAD: 3, OTHER: 4})
        retain_heads(self.last, [{'head': {'sha': HEAD}}, {'number': 4}])
        self.assertEqual(list(self.last), [HEAD])

    def test_retain_heads_drops_the_counts_with_the_head(self):
        self.last.counts.update({HEAD: 3, OTHER: 4})
        retain_heads(self.last, [{'head': {'sha': HEAD}}])
        self.assertEqual(self.last.counts, {HEAD: 3})


class Budget(Fixture, unittest.TestCase):
    def admitted_cycle(self, sha=HEAD):
        self.publisher.status(sha, 'pending', IN_PROGRESS)
        self.publisher.status(sha, 'success', 'ok')

    def test_a_head_below_the_budget_still_publishes_marker_and_success_every_cycle(self):
        self.reader.statuses = spent(STATUS_BUDGET - 4)
        for _ in range(2):
            self.admitted_cycle()
        self.assertEqual(self.states(), ['pending', 'success'] * 2)

    def test_crossing_the_budget_publishes_exactly_one_final_failure(self):
        self.reader.statuses = spent(STATUS_BUDGET - 1)
        for _ in range(3):
            self.admitted_cycle()
        self.assertEqual(self.states(), ['pending', 'failure'])

    def test_the_final_failure_names_the_exhausted_window_and_fits_a_status_description(self):
        self.reader.statuses = spent(STATUS_BUDGET)
        self.admitted_cycle()
        self.assertEqual(self.issuer.posted, [(HEAD, 'failure', EXHAUSTED)])

    def test_a_success_evaluated_after_exhaustion_is_not_published(self):
        self.reader.statuses = spent(STATUS_BUDGET)
        self.admitted_cycle()
        self.publisher.status(HEAD, 'success', 'ok')
        self.assertNotIn('success', self.states())

    def test_nothing_at_all_is_published_after_exhaustion(self):
        self.reader.statuses = spent(STATUS_BUDGET)
        self.admitted_cycle()
        for state, text in (('failure', 'other'), ('pending', 'Waiting for required check Build')):
            self.publisher.status(HEAD, state, text)
        self.assertEqual(len(self.issuer.posted), 1)

    def test_exhaustion_survives_the_record_expiring(self):
        self.reader.statuses = spent(STATUS_BUDGET)
        self.admitted_cycle()
        self.clock.now += RECORD_TTL * 3
        self.publisher.status(HEAD, 'failure', EXHAUSTED)
        self.publisher.status(HEAD, 'success', 'ok')
        self.assertEqual(len(self.issuer.posted), 1)

    def test_the_count_survives_the_record_expiring(self):
        self.reader.statuses = spent(STATUS_BUDGET - 3)
        self.admitted_cycle()
        self.clock.now += RECORD_TTL * 3
        self.admitted_cycle()
        self.assertEqual(self.states(), ['pending', 'success', 'pending', 'failure'])

    def test_a_restart_over_the_budget_publishes_at_most_one_failure_and_never_a_success(self):
        self.restarted(FakeReader(spent(STATUS_BUDGET + 3)))
        for _ in range(3):
            self.admitted_cycle()
        self.assertEqual(self.states(), ['failure'])

    def test_a_restart_after_the_final_failure_publishes_nothing(self):
        self.restarted(FakeReader(spent(STATUS_BUDGET + 1, 'failure', EXHAUSTED)))
        self.admitted_cycle()
        self.assertEqual(self.issuer.posted, [])

    def test_an_exhaustion_text_from_another_creator_is_not_the_final_failure(self):
        listing = spent(STATUS_BUDGET + 1, 'failure', EXHAUSTED)
        listing[0]['creator'] = {'login': 'someone-else'}
        self.restarted(FakeReader(listing))
        self.admitted_cycle()
        self.assertEqual(self.states(), ['failure'])

    def test_an_unreadable_count_publishes_one_failure_saying_so(self):
        self.restarted(FakeReader(error=AdmissionError('GitHub down')))
        for _ in range(3):
            self.admitted_cycle()
        self.assertEqual(self.issuer.posted, [(HEAD, 'failure', UNCOUNTED)])

    def test_a_malformed_count_response_never_publishes_a_success(self):
        self.restarted(FakeReader())
        self.reader.request = lambda method, path: {'message': 'nope'}
        self.admitted_cycle()
        self.assertEqual(self.states(), ['failure'])

    def test_a_head_is_admitted_again_once_its_count_is_readable(self):
        self.restarted(FakeReader(error=AdmissionError('GitHub down')))
        self.admitted_cycle()
        self.reader.error = None
        self.admitted_cycle()
        self.assertEqual(self.states(), ['failure', 'success'])

    def test_a_publication_that_raised_makes_the_next_one_read_the_count_again(self):
        self.issuer.errors[('pending', IN_PROGRESS)] = AdmissionError('blip')
        with self.assertRaises(AdmissionError):
            self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        self.publisher.status(HEAD, 'success', 'ok')
        self.assertEqual(len(self.reader.paths), 2)

    def test_an_outage_of_the_final_failure_never_ends_a_head_holding_a_success(self):
        self.reader.statuses = spent(STATUS_BUDGET)
        self.issuer.errors[('failure', EXHAUSTED)] = AdmissionError('outage')
        for _ in range(STATUS_LIMIT - STATUS_BUDGET + 5):
            with self.assertRaises(AdmissionError):
                self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        self.issuer.errors.clear()
        self.admitted_cycle()
        self.assertEqual(self.issuer.posted[-1], (HEAD, 'failure', EXHAUSTED))

    def test_the_final_failure_is_published_once_after_the_outage(self):
        self.reader.statuses = spent(STATUS_BUDGET)
        self.issuer.errors[('failure', EXHAUSTED)] = AdmissionError('outage')
        with self.assertRaises(AdmissionError):
            self.admitted_cycle()
        self.issuer.errors.clear()
        for _ in range(3):
            self.admitted_cycle()
        self.assertEqual(self.states(), ['failure', 'failure'])

    def test_a_head_at_github_limit_is_left_alone(self):
        self.reader.statuses = spent(STATUS_LIMIT)
        self.admitted_cycle()
        self.assertEqual(self.issuer.posted, [])

    def test_a_different_head_is_unaffected(self):
        self.reader.statuses = spent(STATUS_BUDGET)
        self.admitted_cycle()
        self.reader.statuses = []
        self.admitted_cycle(OTHER)
        self.assertEqual(self.issuer.posted[1:], [(OTHER, 'pending', IN_PROGRESS), (OTHER, 'success', 'ok')])


class CheckDeadline(Fixture, unittest.TestCase):
    WAITING = 'Waiting for required check Build'

    def wait(self, text=WAITING):
        self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        self.publisher.status(HEAD, 'pending', text)

    def test_waiting_under_the_deadline_stays_pending(self):
        self.wait()
        self.clock.now += CHECK_DEADLINE - 1
        self.wait()
        self.assertEqual(set(self.states()), {'pending'})

    def test_the_deadline_publishes_a_failure_naming_the_check(self):
        self.wait()
        self.clock.now += CHECK_DEADLINE
        self.wait()
        text = 'required check Build has not reported after 60 minutes'
        self.assertEqual(self.issuer.posted[-1], (HEAD, 'failure', text))

    def test_the_deadline_failure_is_not_republished_while_waiting_continues(self):
        self.wait()
        self.clock.now += CHECK_DEADLINE
        for _ in range(3):
            self.wait()
        self.assertEqual(self.states().count('failure'), 1)

    def test_the_check_reporting_lets_the_head_be_admitted(self):
        self.wait()
        self.clock.now += CHECK_DEADLINE
        self.wait()
        self.publisher.status(HEAD, 'pending', IN_PROGRESS)
        self.publisher.status(HEAD, 'success', 'ok')
        self.assertEqual(self.issuer.posted[-1], (HEAD, 'success', 'ok'))

    def test_a_check_that_reported_and_waits_again_restarts_the_clock(self):
        self.wait()
        self.clock.now += CHECK_DEADLINE - 1
        self.publisher.status(HEAD, 'success', 'ok')
        self.clock.now += 5
        self.wait()
        self.assertEqual(self.states()[-1], 'pending')

    def test_a_different_unfinished_check_restarts_the_clock(self):
        self.wait()
        self.clock.now += CHECK_DEADLINE - 1
        self.wait('Waiting for required check Test')
        self.clock.now += 5
        self.wait('Waiting for required check Test')
        self.assertEqual(self.issuer.posted[-1][1:], ('pending', 'Waiting for required check Test'))

    def test_the_review_waiting_state_never_times_out(self):
        text = 'Awaiting an independent exact-head agent review'
        self.wait(text)
        self.clock.now += CHECK_DEADLINE * 10
        self.wait(text)
        self.assertEqual(set(self.states()), {'pending'})

    def test_a_restart_resets_the_clock(self):
        self.wait()
        self.clock.now += CHECK_DEADLINE
        self.restarted(FakeReader())
        self.wait()
        self.assertEqual(self.issuer.posted[-1][1], 'pending')

    def test_a_different_head_has_its_own_clock(self):
        self.wait()
        self.clock.now += CHECK_DEADLINE
        self.publisher.status(OTHER, 'pending', self.WAITING)
        self.assertEqual(self.issuer.posted[-1], (OTHER, 'pending', self.WAITING))
