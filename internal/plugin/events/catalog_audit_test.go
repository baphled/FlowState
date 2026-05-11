package events_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/eventlogger"
	"github.com/baphled/flowstate/internal/plugin/sessionrecorder"
)

// catalog_audit_test asserts that catalog.Subscribers claims for the two
// built-in subscriber plugins (eventlogger, sessionrecorder) match the
// authoritative subscribed-event lists exposed by those packages.
//
// The catalog historically over-claimed subscription for events that neither
// plugin actually wired into its bus.Subscribe loop (bug hunt #51, May 2026).
// This audit prevents drift by reading the real subscription source-of-truth
// at test time.

var _ = Describe("catalog Subscribers accuracy", func() {
	var (
		eventLoggerSubs    map[string]struct{}
		sessionRecorderSub map[string]struct{}
	)

	BeforeEach(func() {
		eventLoggerSubs = toSet(eventlogger.SubscribedEventTypes())
		sessionRecorderSub = toSet(sessionrecorder.SubscribedEventTypes())
	})

	Describe("eventlogger claims", func() {
		It("only appears in Subscribers when the topic is in eventlogger.SubscribedEventTypes", func() {
			var mismatches []string
			for _, entry := range events.Catalog {
				if !containsSubscriber(entry.Subscribers, "eventlogger") {
					continue
				}
				if _, ok := eventLoggerSubs[entry.Topic]; !ok {
					mismatches = append(mismatches, fmt.Sprintf(
						"  Catalog entry %q (%s) claims eventlogger subscribes but eventlogger.SubscribedEventTypes does not include it",
						entry.Constant, entry.Topic))
				}
			}
			Expect(mismatches).To(BeEmpty(),
				"eventlogger over-claim(s) in Catalog:\n%s", joinLines(mismatches))
		})

		It("appears in Subscribers for every topic in eventlogger.SubscribedEventTypes", func() {
			var missing []string
			for topic := range eventLoggerSubs {
				entry, found := findCatalogEntry(topic)
				if !found {
					missing = append(missing, fmt.Sprintf(
						"  eventlogger subscribes to %q but no Catalog entry exists", topic))
					continue
				}
				if !containsSubscriber(entry.Subscribers, "eventlogger") {
					missing = append(missing, fmt.Sprintf(
						"  eventlogger subscribes to %q but Catalog entry %q does not list it",
						topic, entry.Constant))
				}
			}
			Expect(missing).To(BeEmpty(),
				"eventlogger missing from Catalog Subscribers:\n%s", joinLines(missing))
		})
	})

	Describe("sessionrecorder claims", func() {
		It("only appears in Subscribers when the topic is in sessionrecorder.SubscribedEventTypes", func() {
			var mismatches []string
			for _, entry := range events.Catalog {
				if !containsSubscriber(entry.Subscribers, "sessionrecorder") {
					continue
				}
				if _, ok := sessionRecorderSub[entry.Topic]; !ok {
					mismatches = append(mismatches, fmt.Sprintf(
						"  Catalog entry %q (%s) claims sessionrecorder subscribes but sessionrecorder.SubscribedEventTypes does not include it",
						entry.Constant, entry.Topic))
				}
			}
			Expect(mismatches).To(BeEmpty(),
				"sessionrecorder over-claim(s) in Catalog:\n%s", joinLines(mismatches))
		})

		It("appears in Subscribers for every topic in sessionrecorder.SubscribedEventTypes", func() {
			var missing []string
			for topic := range sessionRecorderSub {
				entry, found := findCatalogEntry(topic)
				if !found {
					missing = append(missing, fmt.Sprintf(
						"  sessionrecorder subscribes to %q but no Catalog entry exists", topic))
					continue
				}
				if !containsSubscriber(entry.Subscribers, "sessionrecorder") {
					missing = append(missing, fmt.Sprintf(
						"  sessionrecorder subscribes to %q but Catalog entry %q does not list it",
						topic, entry.Constant))
				}
			}
			Expect(missing).To(BeEmpty(),
				"sessionrecorder missing from Catalog Subscribers:\n%s", joinLines(missing))
		})
	})
})

// joinLines joins a slice of lines with newlines, returning an empty string
// when the slice is empty so Gomega's failure message remains tidy.
func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

// toSet converts a slice of topic strings into a lookup set.
func toSet(topics []string) map[string]struct{} {
	out := make(map[string]struct{}, len(topics))
	for _, t := range topics {
		out[t] = struct{}{}
	}
	return out
}

// containsSubscriber returns true when subscribers contains a token whose
// value equals or has the given logical name as a prefix segment.
// Catalog entries use values like "eventlogger" or longer forms such as
// "internal/plugin/eventlogger/eventlogger.go"; both should match the
// logical name "eventlogger".
func containsSubscriber(subscribers []string, name string) bool {
	for _, s := range subscribers {
		if s == name {
			return true
		}
		// Tolerate fully-qualified path forms.
		if len(s) >= len(name) && containsToken(s, name) {
			return true
		}
	}
	return false
}

// containsToken returns true when needle appears as a substring of haystack.
// We accept substring matching because the catalog mixes short and
// fully-qualified subscriber identifiers (e.g. "eventlogger" vs
// "internal/plugin/eventlogger/eventlogger.go").
func containsToken(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// findCatalogEntry returns the catalog entry for a topic or (zero, false).
func findCatalogEntry(topic string) (events.EventCatalogEntry, bool) {
	for _, entry := range events.Catalog {
		if entry.Topic == topic {
			return entry, true
		}
	}
	return events.EventCatalogEntry{}, false
}
