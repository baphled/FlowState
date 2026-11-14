"""Workflow wiring contracts for the review-handoff tool."""
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def ci_next():
    return (ROOT / '.github/workflows/ci-next.yml').read_text()


def handoff_job():
    source = ci_next()
    marker = '  review-handoff:\n'
    if marker not in source:
        raise AssertionError('ci-next.yml has no review-handoff job')
    return source.split(marker, 1)[1].split('\n  \w', 1)[0]


class ReviewHandoffWorkflow(unittest.TestCase):
    def test_ci_next_declares_review_handoff_job(self):
        job = handoff_job()
        self.assertIn('Review Handoff', job)

    def test_review_handoff_job_runs_the_focused_test_files(self):
        job = handoff_job()
        for name in ('test_review_handoff.py',
                     'test_review_handoff_io.py',
                     'test_review_handoff_net.py'):
            self.assertIn(name, job,
                          'focused handoff test file must be pinned, not globbed')

    def test_review_handoff_job_proves_fail_closed_cli(self):
        job = handoff_job()
        self.assertIn('review_handoff.py', job)
        self.assertIn('failed closed', job)


if __name__ == '__main__':
    unittest.main()
