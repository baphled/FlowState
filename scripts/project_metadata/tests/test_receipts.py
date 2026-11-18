"""Owning tests for attempt-receipt schema validation."""
import unittest

from scripts.project_metadata.receipts import manual_errors


class ReceiptSchema(unittest.TestCase):
    def test_wrong_record_type_is_reported(self):
        errors = list(manual_errors({}))
        self.assertTrue(any('record_type' in e for e in errors))


if __name__ == '__main__':
    unittest.main()
