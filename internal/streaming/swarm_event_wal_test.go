package streaming_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

// The SwarmEvent JSONL WAL tests cover the P4 append-on-write durability
// model: every Append writes a single JSONL line to the session event file
// and syncs it to disk before returning. The compact path rewrites the file
// atomically via an fsync-and-rename dance so that bounded compaction never
// leaves the on-disk file in a torn state.
var _ = Describe("SwarmEvent WAL (P4)", func() {
	// refTime fixed to UTC for deterministic round-trip assertions.
	refTime := time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC)

	Describe("AppendSwarmEvent", func() {
		It("writes one line to the file", func() {
			dir, err := os.MkdirTemp("", "swarmwal-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(dir) })

			path := filepath.Join(dir, "sess.events.jsonl")
			ev := streaming.SwarmEvent{
				ID:            "wal-1",
				Type:          streaming.EventToolCall,
				Status:        "started",
				Timestamp:     refTime,
				AgentID:       "engineer",
				SchemaVersion: streaming.CurrentSchemaVersion,
			}

			Expect(streaming.AppendSwarmEvent(path, ev)).To(Succeed())

			data, readErr := os.ReadFile(path)
			Expect(readErr).NotTo(HaveOccurred())
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			Expect(lines).To(HaveLen(1))
			Expect(lines[0]).To(ContainSubstring(`"id":"wal-1"`))
			Expect(lines[0]).To(ContainSubstring(`"tool_call"`))
			Expect(lines[0]).To(HaveSuffix("}"))
		})

		It("appends multiple events in order", func() {
			dir, err := os.MkdirTemp("", "swarmwal-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(dir) })

			path := filepath.Join(dir, "sess.events.jsonl")
			for i, id := range []string{"a", "b", "c"} {
				ev := streaming.SwarmEvent{
					ID:            id,
					Type:          streaming.EventToolCall,
					Status:        "started",
					Timestamp:     refTime.Add(time.Duration(i) * time.Second),
					AgentID:       "engineer",
					SchemaVersion: streaming.CurrentSchemaVersion,
				}
				Expect(streaming.AppendSwarmEvent(path, ev)).To(Succeed())
			}

			data, readErr := os.ReadFile(path)
			Expect(readErr).NotTo(HaveOccurred())
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			Expect(lines).To(HaveLen(3))
			Expect(lines[0]).To(ContainSubstring(`"id":"a"`))
			Expect(lines[1]).To(ContainSubstring(`"id":"b"`))
			Expect(lines[2]).To(ContainSubstring(`"id":"c"`))
		})

		It("is durable (fsync called) via the sync hook", func() {
			dir, err := os.MkdirTemp("", "swarmwal-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(dir) })

			path := filepath.Join(dir, "sess.events.jsonl")
			ev := streaming.SwarmEvent{
				ID:            "fsync",
				Type:          streaming.EventToolCall,
				Status:        "started",
				Timestamp:     refTime,
				AgentID:       "engineer",
				SchemaVersion: streaming.CurrentSchemaVersion,
			}

			// Install a hook that records fsync invocations. Cleanup restores
			// the default no-op hook so later tests are unaffected.
			calls := 0
			prev := streaming.SetSyncHookForTest(func() { calls++ })
			DeferCleanup(func() { streaming.SetSyncHookForTest(prev) })

			Expect(streaming.AppendSwarmEvent(path, ev)).To(Succeed())
			Expect(calls).To(BeNumerically(">=", 1),
				"AppendSwarmEvent must fsync before returning to guarantee durability")
		})

		It("creates the file when it does not exist", func() {
			dir, err := os.MkdirTemp("", "swarmwal-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(dir) })

			path := filepath.Join(dir, "new.events.jsonl")
			ev := streaming.SwarmEvent{
				ID:            "new",
				Type:          streaming.EventPlan,
				Status:        "completed",
				Timestamp:     refTime,
				AgentID:       "planner",
				SchemaVersion: streaming.CurrentSchemaVersion,
			}
			Expect(path).NotTo(BeAnExistingFile())
			Expect(streaming.AppendSwarmEvent(path, ev)).To(Succeed())
			Expect(path).To(BeAnExistingFile())
		})
	})

	Describe("ReadEventsJSONL 1 MiB tolerance", func() {
		It("reads a single line approaching 1 MiB without truncation", func() {
			// Build metadata carrying a ~700 KiB blob to exercise scanner buffer.
			big := strings.Repeat("x", 700*1024)
			ev := streaming.SwarmEvent{
				ID:            "big-meta",
				Type:          streaming.EventToolResult,
				Status:        "completed",
				Timestamp:     refTime,
				AgentID:       "engineer",
				Metadata:      map[string]interface{}{"content": big},
				SchemaVersion: streaming.CurrentSchemaVersion,
			}

			var buf bytes.Buffer
			Expect(streaming.WriteEventsJSONL(&buf, []streaming.SwarmEvent{ev})).To(Succeed())
			Expect(buf.Len()).To(BeNumerically(">", 700*1024),
				"fixture must exceed the default 64 KiB bufio.Scanner limit so the test exercises the raised buffer")

			restored, err := streaming.ReadEventsJSONL(&buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(HaveLen(1))
			content, ok := restored[0].Metadata["content"].(string)
			Expect(ok).To(BeTrue())
			Expect(content).To(HaveLen(len(big)))
		})
	})

	Describe("ReadEventsJSONL malformed-line counter", func() {
		It("counts malformed lines and increments the package counter", func() {
			before := streaming.MalformedLineCount()
			input := "garbage\n{broken\n"
			events, err := streaming.ReadEventsJSONL(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(BeEmpty())
			after := streaming.MalformedLineCount()
			Expect(after-before).To(BeNumerically("==", 2),
				"two malformed lines must each bump the counter so operators can detect corruption")
		})
	})

	Describe("Schema version handling", func() {
		It("round-trips SchemaVersion=1 through JSONL", func() {
			ev := streaming.SwarmEvent{
				ID:            "sv1",
				Type:          streaming.EventToolCall,
				Status:        "started",
				Timestamp:     refTime,
				AgentID:       "engineer",
				SchemaVersion: 1,
			}

			var buf bytes.Buffer
			Expect(streaming.WriteEventsJSONL(&buf, []streaming.SwarmEvent{ev})).To(Succeed())
			Expect(buf.String()).To(ContainSubstring(`"schema_version":1`))

			restored, err := streaming.ReadEventsJSONL(&buf)
			Expect(err).NotTo(HaveOccurred())
			Expect(restored).To(HaveLen(1))
			Expect(restored[0].SchemaVersion).To(Equal(1))
		})

		It("accepts events written without schema_version (legacy files)", func() {
			// Legacy event: no schema_version field. json.Unmarshal returns
			// 0 for the missing int. We accept 0 as implicit v1 for now.
			legacy := `{"id":"legacy","type":"tool_call","status":"started","timestamp":"2026-04-17T09:00:00Z","agent_id":"engineer"}` + "\n"
			events, err := streaming.ReadEventsJSONL(strings.NewReader(legacy))
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(HaveLen(1))
			Expect(events[0].SchemaVersion).To(Equal(0),
				"legacy files decode to zero-value SchemaVersion; the loader treats 0 as implicit v1")
			Expect(events[0].ID).To(Equal("legacy"))
		})

		It("accepts events written with a future schema_version without discarding them", func() {
			// Future schema version: loader must not silently drop the event;
			// it logs a warning (counter-only assertion here) and passes through.
			futureEv := map[string]interface{}{
				"id":             "future",
				"type":           "tool_call",
				"status":         "started",
				"timestamp":      "2026-04-17T09:00:00Z",
				"agent_id":       "engineer",
				"schema_version": 99,
			}
			line, err := json.Marshal(futureEv)
			Expect(err).NotTo(HaveOccurred())

			beforeFuture := streaming.FutureSchemaLineCount()
			events, err := streaming.ReadEventsJSONL(strings.NewReader(string(line) + "\n"))
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(HaveLen(1))
			Expect(events[0].SchemaVersion).To(Equal(99))
			afterFuture := streaming.FutureSchemaLineCount()
			Expect(afterFuture-beforeFuture).To(BeNumerically("==", 1),
				"future schema versions must bump the forward-compat counter so operators can notice upgrades")
		})
	})

	Describe("CompactSwarmEvents", func() {
		It("rewrites the file from the in-memory snapshot atomically", func() {
			dir, err := os.MkdirTemp("", "swarmcompact-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(dir) })

			path := filepath.Join(dir, "sess.events.jsonl")
			// First WAL-append two events to simulate live streaming.
			for i, id := range []string{"a", "b"} {
				Expect(streaming.AppendSwarmEvent(path, streaming.SwarmEvent{
					ID:            id,
					Type:          streaming.EventToolCall,
					Status:        "started",
					Timestamp:     refTime.Add(time.Duration(i) * time.Second),
					AgentID:       "engineer",
					SchemaVersion: streaming.CurrentSchemaVersion,
				})).To(Succeed())
			}

			// Compact to a snapshot of only one event (simulating eviction).
			snapshot := []streaming.SwarmEvent{
				{
					ID:            "final",
					Type:          streaming.EventToolCall,
					Status:        "completed",
					Timestamp:     refTime.Add(5 * time.Second),
					AgentID:       "engineer",
					SchemaVersion: streaming.CurrentSchemaVersion,
				},
			}
			Expect(streaming.CompactSwarmEvents(path, snapshot)).To(Succeed())

			// No leftover .tmp file after a successful compaction.
			Expect(path + ".tmp").NotTo(BeAnExistingFile())

			data, readErr := os.ReadFile(path)
			Expect(readErr).NotTo(HaveOccurred())
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			Expect(lines).To(HaveLen(1))
			Expect(lines[0]).To(ContainSubstring(`"id":"final"`))
		})

		It("syncs the temp file before rename", func() {
			dir, err := os.MkdirTemp("", "swarmcompact-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(dir) })

			path := filepath.Join(dir, "sess.events.jsonl")
			snapshot := []streaming.SwarmEvent{
				{
					ID:            "only",
					Type:          streaming.EventPlan,
					Status:        "completed",
					Timestamp:     refTime,
					AgentID:       "planner",
					SchemaVersion: streaming.CurrentSchemaVersion,
				},
			}

			calls := 0
			prev := streaming.SetSyncHookForTest(func() { calls++ })
			DeferCleanup(func() { streaming.SetSyncHookForTest(prev) })

			Expect(streaming.CompactSwarmEvents(path, snapshot)).To(Succeed())
			Expect(calls).To(BeNumerically(">=", 1),
				"CompactSwarmEvents must fsync the temp file before rename")
		})

		It("removes the file when compacting an empty snapshot", func() {
			dir, err := os.MkdirTemp("", "swarmcompact-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(dir) })

			path := filepath.Join(dir, "sess.events.jsonl")
			Expect(streaming.AppendSwarmEvent(path, streaming.SwarmEvent{
				ID:            "orphan",
				Type:          streaming.EventToolCall,
				Status:        "started",
				Timestamp:     refTime,
				AgentID:       "engineer",
				SchemaVersion: streaming.CurrentSchemaVersion,
			})).To(Succeed())
			Expect(path).To(BeAnExistingFile())

			// Compacting with an empty snapshot collapses the WAL to an empty
			// file: callers explicitly want the event stream replaced with
			// nothing. The existing PersistSwarmEvents contract treated empty
			// as "no-op and leave the file alone"; compact's contract is
			// different because it is the authoritative snapshot on close.
			Expect(streaming.CompactSwarmEvents(path, nil)).To(Succeed())
			info, statErr := os.Stat(path)
			if statErr == nil {
				Expect(info.Size()).To(BeNumerically("==", 0),
					"compaction of an empty snapshot must not leave stale events behind")
			}
		})
	})

	Describe("Round-trip preserves UTC timestamps", func() {
		It("keeps the UTC location across append + read", func() {
			dir, err := os.MkdirTemp("", "swarmutc-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(dir) })

			path := filepath.Join(dir, "sess.events.jsonl")
			ev := streaming.SwarmEvent{
				ID:            "utc",
				Type:          streaming.EventToolCall,
				Status:        "started",
				Timestamp:     time.Now().UTC(),
				AgentID:       "engineer",
				SchemaVersion: streaming.CurrentSchemaVersion,
			}
			Expect(streaming.AppendSwarmEvent(path, ev)).To(Succeed())

			f, openErr := os.Open(path)
			Expect(openErr).NotTo(HaveOccurred())
			defer f.Close()

			events, readErr := streaming.ReadEventsJSONL(f)
			Expect(readErr).NotTo(HaveOccurred())
			Expect(events).To(HaveLen(1))
			// RFC3339 'Z' suffix signals UTC; the decoded time's offset must be zero.
			_, offset := events[0].Timestamp.Zone()
			Expect(offset).To(Equal(0),
				"UTC timestamps must round-trip with a zero zone offset")
		})
	})
})
