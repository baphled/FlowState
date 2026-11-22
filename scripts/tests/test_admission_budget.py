"""read_spent counts the context's statuses on a commit; the budget leaves room below GitHub's limit."""
import unittest

from scripts.admission_budget import EXHAUSTED, STATUS_BUDGET, STATUS_LIMIT, is_final, read_spent
from scripts.admission_metadata import AdmissionError
from scripts.tests.test_admission_publish import HEAD, FakeReader, spent


class ReadSpent(unittest.TestCase):
    def test_a_small_head_costs_one_request(self):
        reader = FakeReader(spent(3))
        read_spent(reader, HEAD)
        self.assertEqual(reader.paths, [f'/commits/{HEAD}/statuses?per_page=100&page=1'])

    def test_the_count_is_returned(self):
        self.assertEqual(read_spent(FakeReader(spent(3)), HEAD)[0], 3)

    def test_other_contexts_are_not_counted(self):
        self.assertEqual(read_spent(FakeReader(spent(250, context='ci/other')), HEAD)[0], 0)

    def test_the_listing_is_paged_until_a_short_page(self):
        reader = FakeReader(spent(STATUS_BUDGET + 50))
        read_spent(reader, HEAD)
        self.assertEqual(len(reader.paths), STATUS_BUDGET // 100 + 1)

    def test_the_final_failure_as_newest_status_is_recognised(self):
        self.assertTrue(read_spent(FakeReader(spent(5, 'failure', EXHAUSTED)), HEAD)[1])

    def test_an_older_final_failure_is_not_the_newest(self):
        self.assertFalse(read_spent(FakeReader(spent(5, 'success')), HEAD)[1])

    def test_no_status_is_not_final(self):
        self.assertFalse(is_final(None))

    def test_more_pages_than_the_bound_raise(self):
        with self.assertRaises(AdmissionError):
            read_spent(FakeReader(spent(100 * 31)), HEAD)

    def test_a_non_list_response_raises(self):
        reader = FakeReader()
        reader.request = lambda method, path: {}
        with self.assertRaises(AdmissionError):
            read_spent(reader, HEAD)


class Limits(unittest.TestCase):
    def test_the_final_failure_description_is_under_140_characters(self):
        self.assertLess(len(EXHAUSTED), 140)

    def test_the_budget_leaves_room_below_github_limit_for_the_final_failure(self):
        self.assertLessEqual(STATUS_BUDGET + 100, STATUS_LIMIT)
