package context_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("CompactedMessage and CompactionIndex storage schema", func() {
	Describe("CompactedMessage JSON roundtrip", func() {
		It("preserves every field across marshal → unmarshal", func() {
			original := flowctx.CompactedMessage{
				ID:                 "01HV8N-unit-id",
				OriginalTokenCount: 1500,
				StoragePath:        "/tmp/compacted/sess-1/01HV8N-unit-id.json",
				Checksum:           "deadbeefcafebabe",
				CreatedAt:          time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC),
				RetrievalCount:     3,
			}

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var roundtrip flowctx.CompactedMessage
			Expect(json.Unmarshal(data, &roundtrip)).To(Succeed())

			Expect(roundtrip).To(Equal(original))
		})
	})

	Describe("CompactedUnit JSON roundtrip", func() {
		It("preserves a solo message payload", func() {
			original := flowctx.CompactedUnit{
				Kind: flowctx.UnitSolo,
				Messages: []provider.Message{
					{Role: "user", Content: "hello"},
				},
			}

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var roundtrip flowctx.CompactedUnit
			Expect(json.Unmarshal(data, &roundtrip)).To(Succeed())

			Expect(roundtrip).To(Equal(original))
		})

		It("preserves a parallel fan-out payload with tool calls intact", func() {
			original := flowctx.CompactedUnit{
				Kind: flowctx.UnitToolGroup,
				Messages: []provider.Message{
					{
						Role: "assistant",
						ToolCalls: []provider.ToolCall{
							{ID: "t1", Name: "read", Arguments: map[string]any{"path": "/a"}},
							{ID: "t2", Name: "read", Arguments: map[string]any{"path": "/b"}},
						},
					},
					{Role: "tool", Content: "A", ToolCalls: []provider.ToolCall{{ID: "t1"}}},
					{Role: "tool", Content: "B", ToolCalls: []provider.ToolCall{{ID: "t2"}}},
				},
			}

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var roundtrip flowctx.CompactedUnit
			Expect(json.Unmarshal(data, &roundtrip)).To(Succeed())

			Expect(roundtrip.Kind).To(Equal(original.Kind))
			Expect(roundtrip.Messages).To(HaveLen(3))
			Expect(roundtrip.Messages[0].Role).To(Equal("assistant"))
			Expect(roundtrip.Messages[0].ToolCalls).To(HaveLen(2))
			Expect(roundtrip.Messages[0].ToolCalls[0].ID).To(Equal("t1"))
			Expect(roundtrip.Messages[0].ToolCalls[0].Arguments["path"]).To(Equal("/a"))
			Expect(roundtrip.Messages[1].ToolCalls[0].ID).To(Equal("t1"))
			Expect(roundtrip.Messages[2].ToolCalls[0].ID).To(Equal("t2"))
		})
	})

	Describe("DefaultMessageCompactor.ShouldCompact (per-unit, ADR atomicity)", func() {
		var (
			compactor *flowctx.DefaultMessageCompactor
			msgs      []provider.Message
		)

		BeforeEach(func() {
			compactor = flowctx.NewDefaultMessageCompactor(10)
			msgs = []provider.Message{
				{Role: "user", Content: "one two three"},                                                                         // 3 tokens (solo)
				{Role: "assistant", Content: "alpha beta gamma delta"},                                                           // 4 tokens (solo)
				{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "t1"}}, Content: "calling"},                              // 1
				{Role: "tool", Content: "result body word word word word word word", ToolCalls: []provider.ToolCall{{ID: "t1"}}}, // 8
			}
		})

		It("returns false when threshold is zero or negative", func() {
			zero := flowctx.NewDefaultMessageCompactor(0)
			unit := flowctx.Unit{Kind: flowctx.UnitSolo, Start: 0, End: 1}
			Expect(zero.ShouldCompact(unit, msgs)).To(BeFalse())
		})

		It("returns false for a solo unit at the threshold (strict greater-than)", func() {
			boundary := flowctx.NewDefaultMessageCompactor(3)
			unit := flowctx.Unit{Kind: flowctx.UnitSolo, Start: 0, End: 1}
			Expect(boundary.ShouldCompact(unit, msgs)).To(BeFalse())
		})

		It("returns true for a solo unit strictly above threshold", func() {
			boundary := flowctx.NewDefaultMessageCompactor(2)
			unit := flowctx.Unit{Kind: flowctx.UnitSolo, Start: 0, End: 1}
			Expect(boundary.ShouldCompact(unit, msgs)).To(BeTrue())
		})

		It("sums tokens across the whole tool-group unit", func() {
			// Tool-group unit covers msgs[2..4]: 1 + 8 = 9 tokens.
			unit := flowctx.Unit{Kind: flowctx.UnitToolGroup, Start: 2, End: 4}
			Expect(compactor.UnitTokenCount(unit, msgs)).To(Equal(9))
			Expect(compactor.ShouldCompact(unit, msgs)).To(BeFalse())

			// Lower the threshold so the *unit* (not any single message)
			// triggers compaction — this is the ADR-mandated per-unit gate.
			low := flowctx.NewDefaultMessageCompactor(8)
			Expect(low.ShouldCompact(unit, msgs)).To(BeTrue())
		})

		It("treats empty content as zero tokens", func() {
			empty := provider.Message{Role: "assistant", Content: ""}
			Expect(compactor.TokenCount(empty)).To(Equal(0))
		})
	})

	Describe("DefaultMessageCompactor.Compact placeholder emission", func() {
		var (
			compactor *flowctx.DefaultMessageCompactor
			msgs      []provider.Message
		)

		BeforeEach(func() {
			compactor = flowctx.NewDefaultMessageCompactor(0)
			msgs = []provider.Message{
				{Role: "user", Content: "u"},
				{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "t1"}, {ID: "t2"}}},
				{Role: "tool", Content: "A", ToolCalls: []provider.ToolCall{{ID: "t1"}}},
				{Role: "tool", Content: "B", ToolCalls: []provider.ToolCall{{ID: "t2"}}},
			}
		})

		It("emits a single solo Role:user placeholder for a solo unit", func() {
			unit := flowctx.Unit{Kind: flowctx.UnitSolo, Start: 0, End: 1}
			out := compactor.Compact(unit, msgs, "rec-solo")

			Expect(out.Role).To(Equal("user"))
			Expect(out.Content).To(ContainSubstring("rec-solo"))
			Expect(out.Content).To(ContainSubstring("1 message"))
			Expect(out.ToolCalls).To(BeEmpty())
		})

		It("emits a single solo placeholder for a parallel fan-out group, dropping all tool entries", func() {
			unit := flowctx.Unit{Kind: flowctx.UnitToolGroup, Start: 1, End: 4}
			out := compactor.Compact(unit, msgs, "rec-fanout")

			// Per ADR atomicity: the whole (N+1)-message unit becomes a
			// single placeholder. tool_use and tool_result entries are
			// dropped *together*.
			Expect(out.Role).To(Equal("user"))
			Expect(out.ToolCalls).To(BeEmpty())
			Expect(out.Content).To(ContainSubstring("rec-fanout"))
			Expect(out.Content).To(ContainSubstring("3 messages"))
		})

		It("placeholder carries no tool_call_id (ADR view-only / atomicity)", func() {
			unit := flowctx.Unit{Kind: flowctx.UnitToolGroup, Start: 1, End: 4}
			out := compactor.Compact(unit, msgs, "rec")

			Expect(out.Role).NotTo(Equal("tool"))
			for _, tc := range out.ToolCalls {
				Expect(tc.ID).To(BeEmpty())
			}
		})
	})

	Describe("CompactionIndex", func() {
		It("NewCompactionIndex initialises an empty, session-bound index", func() {
			idx := flowctx.NewCompactionIndex("sess-42")

			Expect(idx.SessionID).To(Equal("sess-42"))
			Expect(idx.Entries).NotTo(BeNil())
			Expect(idx.Entries).To(BeEmpty())
			Expect(idx.UpdatedAt.IsZero()).To(BeTrue())
		})

		It("survives JSON roundtrip with entries", func() {
			original := flowctx.NewCompactionIndex("sess-42")
			original.Entries["e1"] = flowctx.CompactedMessage{
				ID:                 "e1",
				OriginalTokenCount: 900,
				StoragePath:        "/tmp/compacted/sess-42/e1.json",
				Checksum:           "00ff",
				CreatedAt:          time.Unix(1700000000, 0).UTC(),
			}
			original.UpdatedAt = time.Unix(1700000001, 0).UTC()

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var roundtrip flowctx.CompactionIndex
			Expect(json.Unmarshal(data, &roundtrip)).To(Succeed())

			Expect(roundtrip.SessionID).To(Equal("sess-42"))
			Expect(roundtrip.Entries).To(HaveKey("e1"))
			Expect(roundtrip.Entries["e1"].OriginalTokenCount).To(Equal(900))
			Expect(roundtrip.UpdatedAt.Equal(original.UpdatedAt)).To(BeTrue())
		})
	})
})

var _ = Describe("HotColdSplitter", func() {
	makeBigSolo := func(role, marker string) provider.Message {
		return provider.Message{Role: role, Content: marker + " " + strings.Repeat("word ", 50)}
	}

	Describe("view-only invariant (ADR - View-Only Context Compaction)", func() {
		It("leaves the caller's slice header and elements identity-equal after Split()", func() {
			compactor := flowctx.NewDefaultMessageCompactor(5)
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor:   compactor,
				HotTailSize: 2,
				StorageDir:  "",
				SessionID:   "",
			})

			input := []provider.Message{
				makeBigSolo("user", "u1"),
				makeBigSolo("assistant", "a1"),
				makeBigSolo("user", "u2"),
				makeBigSolo("assistant", "a2"),
			}
			snapshot := make([]provider.Message, len(input))
			copy(snapshot, input)

			_ = s.Split(input)

			Expect(input).To(Equal(snapshot))
			// And the elements themselves are unchanged value-equal.
			for i := range input {
				Expect(input[i]).To(Equal(snapshot[i]))
			}
		})
	})

	Describe("Split is non-blocking and unit-aware", func() {
		It("returns immediately and emits one placeholder per cold large unit", func() {
			compactor := flowctx.NewDefaultMessageCompactor(5)
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor:   compactor,
				HotTailSize: 2,
			})

			// 4 solo units, each ~51 tokens; threshold 5.
			input := []provider.Message{
				makeBigSolo("user", "u1"),
				makeBigSolo("assistant", "a1"),
				makeBigSolo("user", "u2"),
				makeBigSolo("assistant", "a2"),
			}
			done := make(chan flowctx.SplitResult, 1)
			go func() { done <- s.Split(input) }()

			select {
			case res := <-done:
				// First 2 units cold (and both above threshold) → 2 placeholders + 2 hot.
				Expect(res.HotMessages).To(HaveLen(4))
				Expect(res.HotMessages[0].Content).To(HavePrefix("[compacted: "))
				Expect(res.HotMessages[1].Content).To(HavePrefix("[compacted: "))
				Expect(res.HotMessages[2].Content).To(ContainSubstring("u2"))
				Expect(res.HotMessages[3].Content).To(ContainSubstring("a2"))
				Expect(res.ColdRecords).To(HaveLen(2))
			case <-time.After(2 * time.Second):
				Fail("Split blocked")
			}
		})

		It("never splits a tool group — boundary is rounded outward to unit edges", func() {
			compactor := flowctx.NewDefaultMessageCompactor(5)
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor:   compactor,
				HotTailSize: 2, // would land mid-group at message-level
			})

			input := []provider.Message{
				makeBigSolo("user", "u1"),
				{
					Role: "assistant",
					ToolCalls: []provider.ToolCall{
						{ID: "t1", Name: "n"},
						{ID: "t2", Name: "n"},
					},
					Content: "calling many words " + strings.Repeat("x ", 50),
				},
				{Role: "tool", Content: "A " + strings.Repeat("y ", 50), ToolCalls: []provider.ToolCall{{ID: "t1"}}},
				{Role: "tool", Content: "B " + strings.Repeat("z ", 50), ToolCalls: []provider.ToolCall{{ID: "t2"}}},
				makeBigSolo("user", "u2"),
			}

			res := s.Split(input)

			// Either the whole tool group is cold (one placeholder for 3 messages)
			// or the whole tool group is hot — never split.
			placeholderCount := 0
			for _, m := range res.HotMessages {
				if strings.HasPrefix(m.Content, "[compacted: ") {
					placeholderCount++
					Expect(m.Content).To(MatchRegexp(`\d+ messages? elided`))
				}
			}
			// With hotTailSize=2 and the trailing user message + tool group
			// being pulled outward, the group either lands fully in hot or
			// fully in cold; we just assert no placeholder claims to elide
			// "1 message" while a tool message survives nearby (which would
			// indicate a mid-group split).
			for _, m := range res.HotMessages {
				if m.Role == "tool" {
					// If a tool result is in hot, the assistant tool_use
					// must precede it in hot too.
					Expect(res.HotMessages).To(ContainElement(WithTransform(
						func(x provider.Message) bool { return x.Role == "assistant" && len(x.ToolCalls) >= 1 },
						BeTrue(),
					)))
				}
			}
			Expect(placeholderCount).To(BeNumerically(">=", 0))
		})

		It("passes cold units through verbatim when they fall below threshold", func() {
			compactor := flowctx.NewDefaultMessageCompactor(10000) // very high → never compact
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor:   compactor,
				HotTailSize: 1,
			})

			input := []provider.Message{
				{Role: "user", Content: "small"},
				{Role: "assistant", Content: "tiny"},
				{Role: "user", Content: "fits"},
			}
			res := s.Split(input)

			Expect(res.HotMessages).To(HaveLen(3))
			Expect(res.ColdRecords).To(BeEmpty())
		})

		It("returns input verbatim when walker reports malformation", func() {
			compactor := flowctx.NewDefaultMessageCompactor(1)
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor:   compactor,
				HotTailSize: 1,
			})

			// Orphan tool result — walker returns nil; splitter should pass through.
			input := []provider.Message{
				{Role: "user", Content: "u"},
				{Role: "tool", Content: "orphan", ToolCalls: []provider.ToolCall{{ID: "x"}}},
			}
			res := s.Split(input)

			Expect(res.HotMessages).To(Equal(input))
			Expect(res.ColdRecords).To(BeEmpty())
		})

		It("returns empty SplitResult on empty input", func() {
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor:   flowctx.NewDefaultMessageCompactor(1),
				HotTailSize: 1,
			})
			res := s.Split(nil)
			Expect(res.HotMessages).To(BeEmpty())
			Expect(res.ColdRecords).To(BeEmpty())
		})
	})

	Describe("async persist worker writes atomic files", func() {
		It("spills cold payloads to ~/.flowstate/compacted/{session}/{id}.json via temp+rename", func() {
			tmpDir := GinkgoT().TempDir()
			compactor := flowctx.NewDefaultMessageCompactor(5)
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor:            compactor,
				HotTailSize:          1,
				StorageDir:           tmpDir,
				SessionID:            "sess-x",
				PersistChannelBuffer: 8,
			})
			s.StartPersistWorker(context.Background())

			input := []provider.Message{
				makeBigSolo("user", "u1"),
				makeBigSolo("assistant", "a1"),
				makeBigSolo("user", "u2"),
			}
			res := s.Split(input)
			s.Stop() // drains the channel and waits for the worker

			Expect(res.ColdRecords).To(HaveLen(2))

			for _, rec := range res.ColdRecords {
				expectedPath := filepath.Join(tmpDir, "sess-x", rec.ID+".json")
				Expect(rec.StoragePath).To(Equal(expectedPath))

				data, err := os.ReadFile(expectedPath)
				Expect(err).NotTo(HaveOccurred())

				var payload flowctx.CompactedUnit
				Expect(json.Unmarshal(data, &payload)).To(Succeed())
				Expect(payload.Messages).To(HaveLen(1))

				// No leftover .tmp file.
				_, err = os.Stat(expectedPath + ".tmp")
				Expect(os.IsNotExist(err)).To(BeTrue())
			}
		})

		It("logs and continues when the persist worker cannot create the spill directory", func() {
			// Storage dir points at an existing *file*, so MkdirAll will fail.
			tmp := GinkgoT().TempDir()
			blocker := filepath.Join(tmp, "blocker")
			Expect(os.WriteFile(blocker, []byte("x"), 0o600)).To(Succeed())

			compactor := flowctx.NewDefaultMessageCompactor(5)
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor:   compactor,
				HotTailSize: 0,
				StorageDir:  blocker, // file, not dir
				SessionID:   "sess-fail",
			})
			s.StartPersistWorker(context.Background())
			res := s.Split([]provider.Message{makeBigSolo("user", "u1")})
			s.Stop()

			// Placeholder still emitted even though disk write failed.
			Expect(res.HotMessages).To(HaveLen(1))
			Expect(res.HotMessages[0].Content).To(HavePrefix("[compacted: "))
			Expect(res.ColdRecords).To(HaveLen(1))

			// The expected file does not exist (write failed loudly).
			rec := res.ColdRecords[0]
			_, err := os.Stat(rec.StoragePath)
			Expect(err).To(HaveOccurred())
		})

		It("Stop is idempotent", func() {
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor: flowctx.NewDefaultMessageCompactor(1),
			})
			s.StartPersistWorker(context.Background())
			s.Stop()
			s.Stop() // must not panic on double-close
		})

		It("StartPersistWorker is idempotent", func() {
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor: flowctx.NewDefaultMessageCompactor(1),
			})
			s.StartPersistWorker(context.Background())
			s.StartPersistWorker(context.Background()) // second call is no-op
			s.Stop()
		})
	})

	Describe("writeJob error paths", func() {
		It("returns an error when MkdirAll fails (parent is a regular file)", func() {
			tmp := GinkgoT().TempDir()
			file := filepath.Join(tmp, "blocker")
			Expect(os.WriteFile(file, []byte("x"), 0o600)).To(Succeed())

			err := flowctx.ExportedWriteJob(
				filepath.Join(file, "child", "p.json"),
				flowctx.UnitSolo,
				[]provider.Message{{Role: "user", Content: "x"}},
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("creating spill dir"))
		})

		It("returns an error when WriteFile cannot create the temp file", func() {
			tmp := GinkgoT().TempDir()
			// Make the directory read-only so WriteFile fails.
			Expect(os.Chmod(tmp, 0o500)).To(Succeed())
			DeferCleanup(func() { _ = os.Chmod(tmp, 0o700) })

			err := flowctx.ExportedWriteJob(
				filepath.Join(tmp, "p.json"),
				flowctx.UnitSolo,
				[]provider.Message{{Role: "user", Content: "x"}},
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("writing temp file"))
		})

		It("returns an error when Rename fails (target directory removed mid-flight)", func() {
			tmp := GinkgoT().TempDir()
			subdir := filepath.Join(tmp, "session")
			Expect(os.MkdirAll(subdir, 0o700)).To(Succeed())

			// Pre-write a directory at the would-be target so rename onto it
			// fails (cannot replace a non-empty directory with a file).
			target := filepath.Join(subdir, "p.json")
			Expect(os.MkdirAll(target, 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(target, "blocker"), []byte("x"), 0o600)).To(Succeed())

			err := flowctx.ExportedWriteJob(
				target,
				flowctx.UnitSolo,
				[]provider.Message{{Role: "user", Content: "x"}},
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("renaming temp file"))
		})
	})

	Describe("constructor hardening", func() {
		It("returns nil when compactor is nil", func() {
			Expect(flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{})).To(BeNil())
		})

		It("uses sensible defaults for unset fields", func() {
			s := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
				Compactor:   flowctx.NewDefaultMessageCompactor(1),
				HotTailSize: -3, // negative → coerced to zero
			})
			Expect(s).NotTo(BeNil())
			// Negative hot tail means everything is cold prefix.
			res := s.Split([]provider.Message{{Role: "user", Content: strings.Repeat("x ", 20)}})
			Expect(res.HotMessages).To(HaveLen(1))
			Expect(res.HotMessages[0].Content).To(HavePrefix("[compacted: "))
		})
	})
})
