package recall_test

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/recall"
)

// sampleEntries returns a deterministic mix of entry types. Tests that care
// about ordering pass the entries through Retrieve, which owns the sorting
// contract.
func sampleEntries(now time.Time) []recall.KnowledgeEntry {
	return []recall.KnowledgeEntry{
		{ID: "e1", Type: "fact", Content: "API base URL is /v1", ExtractedAt: now, Relevance: 0.9},
		{ID: "e2", Type: "convention", Content: "prefer snake_case in JSON", ExtractedAt: now, Relevance: 0.8},
		{ID: "e3", Type: "preference", Content: "use British English", ExtractedAt: now, Relevance: 0.7},
	}
}

// T13 SessionMemoryStore specification.
//
// SessionMemoryStore is the Phase 3 persistent store for distilled knowledge
// entries extracted from conversation transcripts. It is physically
// co-located with FileContextStore so both share the same atomic
// temp-then-rename write pattern and can be audited together.
var _ = Describe("SessionMemoryStore", func() {
	Describe("Save / Load round-trip", func() {
		It("preserves every entry byte-for-byte (Time fields compared with .Equal)", func() {
			dir := GinkgoT().TempDir()
			store := recall.NewSessionMemoryStore(dir)

			now := time.Date(2026, 4, 14, 10, 30, 0, 0, time.UTC)
			for _, e := range sampleEntries(now) {
				store.AddEntry(e)
			}

			Expect(store.Save("sess-roundtrip")).To(Succeed())

			loaded := recall.NewSessionMemoryStore(dir)
			Expect(loaded.Load("sess-roundtrip")).To(Succeed())

			got := loaded.Entries()
			want := sampleEntries(now)
			Expect(got).To(HaveLen(len(want)))
			for i := range want {
				Expect(got[i].ID).To(Equal(want[i].ID))
				Expect(got[i].Type).To(Equal(want[i].Type))
				Expect(got[i].Content).To(Equal(want[i].Content))
				Expect(got[i].ExtractedAt.Equal(want[i].ExtractedAt)).To(BeTrue())
				Expect(got[i].Relevance).To(Equal(want[i].Relevance))
			}
		})
	})

	Describe("Save", func() {
		It("uses the atomic temp-then-rename pattern (no .tmp residue)", func() {
			dir := GinkgoT().TempDir()
			store := recall.NewSessionMemoryStore(dir)
			store.AddEntry(recall.KnowledgeEntry{ID: "e1", Type: "fact", Content: "x", Relevance: 0.5})

			Expect(store.Save("sess-atomic")).To(Succeed())

			entries, err := os.ReadDir(filepath.Join(dir, "sess-atomic"))
			Expect(err).NotTo(HaveOccurred())
			for _, ent := range entries {
				Expect(filepath.Ext(ent.Name())).NotTo(Equal(".tmp"),
					"atomic contract breached: temp file %q still present", ent.Name())
			}
		})

		It("surfaces a wrapped error when the storage directory is a regular file", func() {
			parent := GinkgoT().TempDir()
			blocker := filepath.Join(parent, "blocker")
			Expect(os.WriteFile(blocker, []byte("regular file"), 0o600)).To(Succeed())

			store := recall.NewSessionMemoryStore(blocker)
			store.AddEntry(recall.KnowledgeEntry{ID: "e1", Type: "fact", Content: "x", Relevance: 0.5})

			Expect(store.Save("any")).To(HaveOccurred())
		})
	})

	Describe("Load", func() {
		It("returns an error sentinel for a missing session", func() {
			store := recall.NewSessionMemoryStore(GinkgoT().TempDir())
			Expect(store.Load("no-such-session")).To(HaveOccurred())
		})

		It("returns an unmarshal error when memory.json is corrupted", func() {
			dir := GinkgoT().TempDir()
			sessDir := filepath.Join(dir, "corrupt-sess")
			Expect(os.MkdirAll(sessDir, 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(sessDir, "memory.json"), []byte("{not valid json"), 0o600)).To(Succeed())

			store := recall.NewSessionMemoryStore(dir)
			Expect(store.Load("corrupt-sess")).To(HaveOccurred())
		})
	})

	Describe("Retrieve", func() {
		It("filters by type, sorts descending by relevance, and drops entries below the 0.3 floor", func() {
			store := recall.NewSessionMemoryStore(GinkgoT().TempDir())
			store.AddEntry(recall.KnowledgeEntry{ID: "f1", Type: "fact", Content: "high-relevance fact", Relevance: 0.9})
			store.AddEntry(recall.KnowledgeEntry{ID: "f2", Type: "fact", Content: "mid-relevance fact", Relevance: 0.6})
			store.AddEntry(recall.KnowledgeEntry{ID: "f3", Type: "fact", Content: "low-relevance fact", Relevance: 0.2}) // filtered out
			store.AddEntry(recall.KnowledgeEntry{ID: "c1", Type: "convention", Content: "conv entry", Relevance: 0.4})

			got := store.Retrieve("fact", 5)
			Expect(got).To(HaveLen(2))
			Expect(got[0].ID).To(Equal("f1"), "highest relevance first")
			Expect(got[1].ID).To(Equal("f2"))
		})

		It("respects the limit cap when more entries qualify", func() {
			store := recall.NewSessionMemoryStore(GinkgoT().TempDir())
			for i, r := range []float64{0.9, 0.8, 0.7, 0.6, 0.5} {
				store.AddEntry(recall.KnowledgeEntry{
					ID:        fmt.Sprintf("f%d", i),
					Type:      "fact",
					Content:   fmt.Sprintf("fact %d", i),
					Relevance: r,
				})
			}

			Expect(store.Retrieve("fact", 3)).To(HaveLen(3))
		})

		It("returns an empty slice for zero or negative limits", func() {
			store := recall.NewSessionMemoryStore(GinkgoT().TempDir())
			store.AddEntry(recall.KnowledgeEntry{ID: "f1", Type: "fact", Content: "x", Relevance: 0.9})

			Expect(store.Retrieve("fact", 0)).To(BeEmpty())
			Expect(store.Retrieve("fact", -5)).To(BeEmpty())
		})
	})

	Describe("AddEntry", func() {
		It("dedupes by content across repeated AddEntry calls", func() {
			store := recall.NewSessionMemoryStore(GinkgoT().TempDir())

			store.AddEntry(recall.KnowledgeEntry{ID: "e1", Type: "fact", Content: "dup", Relevance: 0.5})
			store.AddEntry(recall.KnowledgeEntry{ID: "e2", Type: "fact", Content: "dup", Relevance: 0.6})
			store.AddEntry(recall.KnowledgeEntry{ID: "e3", Type: "convention", Content: "unique", Relevance: 0.4})

			Expect(store.Entries()).To(HaveLen(2),
				"dup must collapse to one + one unique = 2 entries")
		})
	})
})
