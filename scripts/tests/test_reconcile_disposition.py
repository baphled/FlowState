import copy
import unittest
from scripts.reconcile_config import ReconcileError
from scripts.reconcile_disposition import TOPICS, disposition_errors, validate_disposition

DIGEST = "ab" * 32
REASON = ("Nothing in this packet touches this area, so there is no behaviour, record or "
          "artefact for the topic to constrain.")
SENSITIVE = (".github/workflows/release.yml", "go.mod")
EVIDENCE = {"kind": "pull_request", "ref": "dropbox:/FlowState/Metadata/attempts/x/pr.json", "sha256": DIGEST}


def topic_na():
    return {"status": "not-applicable", "statement": REASON}


def topic_applicable():
    return {"status": "applicable", "evidence": [copy.deepcopy(EVIDENCE)]}


def disposition(**changes):
    value = {topic: topic_na() for topic in TOPICS}
    value.update(changes)
    return value


class AcceptedTest(unittest.TestCase):
    def test_twelve_topics_exist(self):
        self.assertEqual(len(set(TOPICS)), 12)

    def test_twelve_not_applicable_topics_are_accepted(self):
        self.assertEqual(disposition_errors(disposition(), ("scripts/a.py",), SENSITIVE), [])

    def test_applicable_topics_with_evidence_are_accepted(self):
        value = disposition(semantic_versioning=topic_applicable())
        self.assertEqual(disposition_errors(value, (".github/workflows/release.yml",), SENSITIVE), [])

    def test_every_evidence_kind_is_accepted(self):
        for kind in ("commit", "pull_request", "workflow", "record"):
            item = dict(EVIDENCE, kind=kind)
            value = disposition(breaking_changes={"status": "applicable", "evidence": [item]})
            with self.subTest(kind=kind):
                self.assertEqual(disposition_errors(value), [])

    def test_validate_returns_none_for_a_valid_disposition(self):
        self.assertIsNone(validate_disposition(disposition()))


class TopicSetTest(unittest.TestCase):
    def test_missing_topic_is_rejected_naming_it(self):
        for topic in TOPICS:
            value = disposition()
            del value[topic]
            with self.subTest(topic=topic):
                self.assertEqual(len(disposition_errors(value)), 1)
                self.assertIn(topic, disposition_errors(value)[0])

    def test_thirteenth_topic_is_rejected_naming_it(self):
        errors = disposition_errors(disposition(thirteenth_topic=topic_na()))
        self.assertEqual((len(errors), "thirteenth_topic" in errors[0]), (1, True))

    def test_non_object_disposition_is_rejected(self):
        for bad in (None, [], "x", 7):
            with self.subTest(bad=bad):
                self.assertTrue(disposition_errors(bad))

    def test_validate_raises_reconcile_error_naming_every_defect(self):
        value = disposition(thirteenth_topic=topic_na())
        del value["breaking_changes"]
        with self.assertRaises(ReconcileError) as caught:
            validate_disposition(value)
        self.assertTrue("breaking_changes" in str(caught.exception) and "thirteenth_topic" in str(caught.exception))


class NotApplicableTest(unittest.TestCase):
    def rejected(self, topic, value):
        errors = disposition_errors(disposition(**{topic: value}))
        return len(errors) == 1 and topic in errors[0]

    def test_two_word_statement_is_rejected(self):
        self.assertTrue(self.rejected("semantic_versioning", {"status": "not-applicable", "statement": "Not needed"}))

    def test_statement_below_eighty_characters_is_rejected(self):
        self.assertTrue(self.rejected("breaking_changes", {"status": "not-applicable", "statement": "x" * 79}))

    def test_padding_whitespace_does_not_count_towards_eighty(self):
        self.assertTrue(self.rejected("breaking_changes", {"status": "not-applicable", "statement": "x" * 10 + " " * 90}))

    def test_exactly_eighty_characters_is_accepted(self):
        self.assertEqual(disposition_errors(disposition(breaking_changes={"status": "not-applicable", "statement": "x" * 80})), [])

    def test_malformed_topic_shapes_are_rejected(self):
        bad = ({"status": "not-applicable"}, {"status": "not-applicable", "statement": 80},
               {"status": "not-applicable", "statement": REASON, "extra": 1},
               {"status": "not-applicable", "statement": REASON, "evidence": [EVIDENCE]},
               {"status": "maybe", "statement": REASON}, {"statement": REASON}, "text", None, [])
        for value in bad:
            with self.subTest(value=value):
                self.assertTrue(self.rejected("dependency_management", value))


class ApplicableTest(unittest.TestCase):
    def rejected(self, evidence):
        errors = disposition_errors(disposition(packaging_distribution={"status": "applicable", "evidence": evidence}))
        return len(errors) == 1 and "packaging_distribution" in errors[0]

    def test_applicable_without_evidence_is_rejected(self):
        self.assertTrue(self.rejected([]))
        self.assertTrue(disposition_errors(disposition(packaging_distribution={"status": "applicable"})))

    def test_evidence_must_be_a_list_of_objects(self):
        for bad in (None, "x", {}, [None], ["x"], [[]]):
            with self.subTest(bad=bad):
                self.assertTrue(self.rejected(bad))

    def test_malformed_evidence_item_is_rejected(self):
        bad = (dict(EVIDENCE, sha256="ab" * 31), dict(EVIDENCE, sha256="ab" * 33), dict(EVIDENCE, sha256="AB" * 32),
               dict(EVIDENCE, sha256="zz" * 32), dict(EVIDENCE, sha256=None), dict(EVIDENCE, kind="issue"),
               dict(EVIDENCE, kind=None), dict(EVIDENCE, ref=""), dict(EVIDENCE, ref=3), dict(EVIDENCE, extra=1),
               {"kind": "commit", "ref": "abc"}, {"ref": "abc", "sha256": DIGEST})
        for item in bad:
            with self.subTest(item=item):
                self.assertTrue(self.rejected([item]))

    def test_one_bad_item_among_good_ones_is_rejected(self):
        self.assertTrue(self.rejected([EVIDENCE, dict(EVIDENCE, sha256="short")]))

    def test_more_than_thirty_two_items_is_rejected(self):
        self.assertTrue(self.rejected([EVIDENCE] * 33))
        self.assertFalse(self.rejected([EVIDENCE] * 32))


class ReleaseSensitiveTest(unittest.TestCase):
    def test_release_workflow_packet_with_every_topic_not_applicable_is_rejected(self):
        errors = disposition_errors(disposition(), (".github/workflows/release.yml",), SENSITIVE)
        self.assertEqual(len(errors), 1)
        self.assertIn("release_sensitive_paths", errors[0])

    def test_one_applicable_topic_satisfies_a_release_sensitive_packet(self):
        value = disposition(release_qualification=topic_applicable())
        self.assertEqual(disposition_errors(value, ("go.mod", "scripts/a.py"), SENSITIVE), [])

    def test_packet_without_a_sensitive_literal_path_is_unconstrained(self):
        for paths in ((), ("scripts/a.py",), (".github/workflows/other.yml",), (".github/workflows/*.yml",)):
            with self.subTest(paths=paths):
                self.assertEqual(disposition_errors(disposition(), paths, SENSITIVE), [])

    def test_wildcard_pattern_is_not_a_literal_even_if_it_covers_a_sensitive_path(self):
        self.assertEqual(disposition_errors(disposition(), ("go.*", "**"), ("go.*", "**")), [])

    def test_empty_sensitive_list_never_requires_applicability(self):
        self.assertEqual(disposition_errors(disposition(), ("go.mod",), ()), [])

    def test_invalid_topic_and_sensitive_path_report_both(self):
        value = disposition(semantic_versioning={"status": "not-applicable", "statement": "no"})
        self.assertEqual(len(disposition_errors(value, ("go.mod",), SENSITIVE)), 2)


if __name__ == "__main__":
    unittest.main()
