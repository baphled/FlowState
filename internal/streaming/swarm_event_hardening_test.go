package streaming_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

// The hardening tests exercise the P6 persistence tail: per-session write
// serialisation, orphan cleanup, metadata deep-copy, unknown-type
// observability, and per-read corruption counts. Each block documents the
// specific failure mode it guards against so future regressions surface
// loudly in test output.
var _ = Describe("SwarmEvent persistence hardening (P6)", func() {
	refTime := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	Describe("per-session write serialisation (T1 / B5)", func() {
		It("serialises concurrent Append + Compact on the same path without corruption", func() {
			dir, err := os.MkdirTemp("", "p6lock-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(dir) })

			path := filepath.Join(dir, "sess.events.jsonl")

			const appenders = 50
			const compactors = 5

			var wg sync.WaitGroup
			wg.Add(appenders + compactors)

			// Race a pool of Append goroutines against a pool of Compact
			// goroutines. Without a per-session lock the compactor's
			// rename-over-path can land between an appender's OpenFile and
			// its Write, producing a file where the rename loses the
			// append's bytes. With the lock, the final Append always
			// observes a well-formed file.
			for i := range appenders {
				go func(idx int) {
					defer wg.Done()
					_ = streaming.AppendSwarmEvent(path, streaming.SwarmEvent{
						ID:            fmt.Sprintf("app-%d", idx),
						Type:          streaming.EventToolCall,
						Status:        "started",
						Timestamp:     refTime.Add(time.Duration(idx) * time.Millisecond),
						AgentID:       "engineer",
						SchemaVersion: streaming.CurrentSchemaVersion,
					})
				}(i)
			}
			for i := range compactors {
				go func(idx int) {
					defer wg.Done()
					snapshot := []streaming.SwarmEvent{
						{
							ID:            fmt.Sprintf("compact-%d", idx),
							Type:          streaming.EventPlan,
							Status:        "completed",
							Timestamp:     refTime,
							AgentID:       "planner",
							SchemaVersion: streaming.CurrentSchemaVersion,
						},
					}
					_ = streaming.CompactSwarmEvents(path, snapshot)
				}(i)
			}
			wg.Wait()

			// The file must be well-formed JSONL: every non-empty line
			// parses. A missing lock manifests as bytes from a concurrent
			// append mid-compaction that end up as a torn line.
			f, openErr := os.Open(path)
			Expect(openErr).NotTo(HaveOccurred())
			DeferCleanup(func() { f.Close() })

			events, stats, readErr := streaming.ReadEventsJSONLWithStats(f)
			Expect(readErr).NotTo(HaveOccurred())
			// The race leaves behind whichever writer finished last; what
			// matters is that there are zero malformed lines. Assert the
			// per-read count because the global counter could be bumped by
			// unrelated tests running in parallel.
			Expect(stats.MalformedLines).To(Equal(int64(0)))
			Expect(events).NotTo(BeNil())
		})

		It("does not serialise writes against distinct paths", func() {
			dir, err := os.MkdirTemp("", "p6lock-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(dir) })

			pathA := filepath.Join(dir, "a.events.jsonl")
			pathB := filepath.Join(dir, "b.events.jsonl")

			// Acquire the lock for pathA and hold it on the test goroutine,
			// then assert that acquiring the lock for pathB from another
			// goroutine completes immediately. If the implementation used a
			// single global mutex instead of per-path, this would deadlock.
			unlockA := streaming.LockPathForTest(pathA)
			DeferCleanup(unlockA)

			done := make(chan struct{})
			go func() {
				unlockB := streaming.LockPathForTest(pathB)
				unlockB()
				close(done)
			}()

			Eventually(done, 2*time.Second).Should(BeClosed(),
				"distinct paths must not share a lock")
		})
	})

	Describe("orphan cleanup (T2 / B8)", func() {
		Describe("RemoveSwarmEvents", func() {
			It("removes both the .events.jsonl and its .tmp sibling", func() {
				dir, err := os.MkdirTemp("", "p6remove-*")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { os.RemoveAll(dir) })

				path := filepath.Join(dir, "sess.events.jsonl")
				Expect(os.WriteFile(path, []byte("{\"id\":\"x\",\"type\":\"plan\"}\n"), 0o600)).To(Succeed())
				Expect(os.WriteFile(path+".tmp", []byte("leftover"), 0o600)).To(Succeed())

				Expect(streaming.RemoveSwarmEvents(path)).To(Succeed())

				Expect(path).NotTo(BeAnExistingFile())
				Expect(path + ".tmp").NotTo(BeAnExistingFile())
			})

			It("is tolerant to a missing file", func() {
				dir, err := os.MkdirTemp("", "p6remove-*")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { os.RemoveAll(dir) })

				path := filepath.Join(dir, "nope.events.jsonl")
				// Calling Remove on a non-existent path must not error —
				// session-delete callers should not have to pre-stat.
				Expect(streaming.RemoveSwarmEvents(path)).To(Succeed())
			})
		})

		Describe("CompactSwarmEvents rename failure", func() {
			It("removes the .tmp file when the rename step fails", func() {
				dir, err := os.MkdirTemp("", "p6rename-*")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { os.RemoveAll(dir) })

				path := filepath.Join(dir, "sess.events.jsonl")
				injectedErr := errors.New("injected rename failure")
				prev := streaming.SetRenameHookForTest(func(_, _ string) error { return injectedErr })
				DeferCleanup(func() { streaming.SetRenameHookForTest(prev) })

				err = streaming.CompactSwarmEvents(path, []streaming.SwarmEvent{
					{
						ID:            "only",
						Type:          streaming.EventPlan,
						Status:        "completed",
						Timestamp:     refTime,
						AgentID:       "planner",
						SchemaVersion: streaming.CurrentSchemaVersion,
					},
				})
				Expect(err).To(MatchError(injectedErr))
				Expect(path+".tmp").NotTo(BeAnExistingFile(),
					".tmp must be cleaned up when rename fails")
				Expect(path).NotTo(BeAnExistingFile(),
					"target path must not exist when the rename failed")
			})
		})

		Describe("CleanupOrphanTmpFiles", func() {
			It("removes any .events.jsonl.tmp files in the directory", func() {
				dir, err := os.MkdirTemp("", "p6scan-*")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { os.RemoveAll(dir) })

				orphan1 := filepath.Join(dir, "sess-a.events.jsonl.tmp")
				orphan2 := filepath.Join(dir, "sess-b.events.jsonl.tmp")
				keep := filepath.Join(dir, "sess-a.events.jsonl")
				unrelated := filepath.Join(dir, "unrelated.tmp")

				for _, p := range []string{orphan1, orphan2, keep, unrelated} {
					Expect(os.WriteFile(p, []byte("x"), 0o600)).To(Succeed())
				}

				removed, err := streaming.CleanupOrphanTmpFiles(dir)
				Expect(err).NotTo(HaveOccurred())
				Expect(removed).To(Equal(2))

				Expect(orphan1).NotTo(BeAnExistingFile())
				Expect(orphan2).NotTo(BeAnExistingFile())
				Expect(keep).To(BeAnExistingFile(),
					"scan must not touch the live events file")
				Expect(unrelated).To(BeAnExistingFile(),
					"scan must not touch files outside the events.jsonl.tmp pattern")
			})

			It("tolerates a missing directory", func() {
				// Startup may run before the session dir exists — make the
				// scan a safe no-op rather than a crash.
				removed, err := streaming.CleanupOrphanTmpFiles(filepath.Join(os.TempDir(), "definitely-does-not-exist-p6"))
				Expect(err).NotTo(HaveOccurred())
				Expect(removed).To(Equal(0))
			})
		})
	})

	Describe("deep-copy Metadata in All() (T3)", func() {
		It("isolates the caller's Metadata mutation from the store", func() {
			store := streaming.NewMemorySwarmStore(200)
			store.Append(streaming.SwarmEvent{
				ID:       "meta-evt",
				Type:     streaming.EventToolCall,
				Metadata: map[string]interface{}{"tool_name": "read"},
			})

			snapshot := store.All()
			Expect(snapshot).To(HaveLen(1))
			// Mutate the returned map: add a new key and overwrite the
			// existing one. Without deep-copy this would race with any
			// concurrent Append reader and corrupt the shared state.
			snapshot[0].Metadata["tool_name"] = "mutated"
			snapshot[0].Metadata["injected"] = true

			again := store.All()
			Expect(again[0].Metadata).To(HaveKeyWithValue("tool_name", "read"))
			Expect(again[0].Metadata).NotTo(HaveKey("injected"))
		})

		It("handles events with nil Metadata without panicking", func() {
			store := streaming.NewMemorySwarmStore(200)
			store.Append(streaming.SwarmEvent{
				ID:   "no-meta",
				Type: streaming.EventPlan,
			})

			snapshot := store.All()
			Expect(snapshot).To(HaveLen(1))
			Expect(snapshot[0].Metadata).To(BeNil())
		})
	})

	Describe("unknown type observability (T4)", func() {
		It("emits slog.Warn and increments the counter on an unknown type", func() {
			records, restore := captureSlog()
			DeferCleanup(restore)

			before := streaming.UnknownTypeLineCount()

			// Craft a JSONL line whose type is not one of the known five.
			input := `{"id":"u1","type":"mystery","status":"x","timestamp":"2026-04-17T12:00:00Z","agent_id":"a"}` + "\n"
			events, err := streaming.ReadEventsJSONL(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			// Event is still returned (forward compat) — the pane's
			// visibility map hides it; this is observability only.
			Expect(events).To(HaveLen(1))

			after := streaming.UnknownTypeLineCount()
			Expect(after - before).To(Equal(int64(1)))

			found := false
			for _, r := range *records {
				if r.Level == slog.LevelWarn && strings.Contains(r.Message, "unknown swarm event type") {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "expected a Warn log for the unknown type")
		})

		It("does not warn or count known types", func() {
			records, restore := captureSlog()
			DeferCleanup(restore)

			before := streaming.UnknownTypeLineCount()
			input := `{"id":"k1","type":"plan","status":"completed","timestamp":"2026-04-17T12:00:00Z","agent_id":"a"}` + "\n"
			_, err := streaming.ReadEventsJSONL(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			Expect(streaming.UnknownTypeLineCount()).To(Equal(before))

			for _, r := range *records {
				Expect(r.Message).NotTo(ContainSubstring("unknown swarm event type"))
			}
		})
	})

	Describe("per-read corruption count (T5)", func() {
		It("reports the number of malformed lines observed on this read", func() {
			// Mix good and corrupt lines so the count is non-zero and
			// distinguishable from the global MalformedLineCount() which
			// accumulates across other tests.
			input := strings.Join([]string{
				`{"id":"g1","type":"plan","timestamp":"2026-04-17T12:00:00Z","agent_id":"a"}`,
				`{this is not json`,
				`{"id":"g2","type":"plan","timestamp":"2026-04-17T12:00:01Z","agent_id":"a"}`,
				`}]]garbage`,
				``, // blank line must not count
			}, "\n") + "\n"

			events, stats, err := streaming.ReadEventsJSONLWithStats(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(HaveLen(2))
			Expect(stats.MalformedLines).To(Equal(int64(2)))
			Expect(stats.UnknownTypeLines).To(Equal(int64(0)))
		})

		It("counts unknown-type lines separately from malformed lines", func() {
			input := `{"id":"u","type":"surprise","timestamp":"2026-04-17T12:00:00Z","agent_id":"a"}` + "\n"
			events, stats, err := streaming.ReadEventsJSONLWithStats(strings.NewReader(input))
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(HaveLen(1))
			Expect(stats.MalformedLines).To(Equal(int64(0)))
			Expect(stats.UnknownTypeLines).To(Equal(int64(1)))
		})
	})
})

// captureSlog installs a recording slog handler and returns a restore closure
// the caller is expected to DeferCleanup. Atomic.Bool ensures the installed
// handler ignores records from unrelated background goroutines after restore.
func captureSlog() (*[]slog.Record, func()) {
	var records []slog.Record
	var active atomic.Bool
	active.Store(true)
	h := &slogCapture{records: &records, active: &active}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	return &records, func() {
		active.Store(false)
		slog.SetDefault(prev)
	}
}

type slogCapture struct {
	records *[]slog.Record
	active  *atomic.Bool
	mu      sync.Mutex
}

func (h *slogCapture) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *slogCapture) Handle(_ context.Context, r slog.Record) error {
	if !h.active.Load() {
		return nil
	}
	h.mu.Lock()
	*h.records = append(*h.records, r)
	h.mu.Unlock()
	return nil
}

func (h *slogCapture) WithAttrs(_ []slog.Attr) slog.Handler { return h }

func (h *slogCapture) WithGroup(_ string) slog.Handler { return h }
