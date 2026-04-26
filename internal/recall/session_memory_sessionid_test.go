// Package recall_test — H4 defence-in-depth coverage for
// SessionMemoryStore.Save and SessionMemoryStore.Load.
//
// The CLI gate in internal/cli/run.go rejects unsafe --session values
// before any store method runs, but SessionMemoryStore is also
// callable from in-process consumers (specs, future internal APIs)
// that bypass the CLI. These specs pin the layered-defence contract:
// every path-unsafe sessionID supplied directly to Save/Load is
// refused with ErrInvalidSessionID, and no filesystem mutation
// occurs.
package recall_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/sessionid"
)

var _ = Describe("SessionMemoryStore sessionID safety (H4)", func() {
	// Save_RejectsUnsafeSessionID pins the refusal. The store never
	// touches the filesystem because the validator fires before
	// filepath.Join is called.
	DescribeTable("Save refuses unsafe sessionIDs without touching the filesystem",
		func(id string) {
			dir := GinkgoT().TempDir()
			store := recall.NewSessionMemoryStore(dir)
			err := store.Save(id)
			Expect(err).To(HaveOccurred(),
				"Save(%q) = nil; want refusal", id)
			Expect(errors.Is(err, sessionid.ErrInvalidSessionID)).To(BeTrue(),
				"Save(%q) err = %v; want errors.Is ErrInvalidSessionID", id, err)

			// No side effect — the root dir must be empty because
			// Save must not call MkdirAll with a tainted path.
			entries, readErr := os.ReadDir(dir)
			Expect(readErr).NotTo(HaveOccurred(), "ReadDir after refused Save")
			Expect(entries).To(BeEmpty(),
				"Save(%q) left %d entries in storageDir; want 0", id, len(entries))
		},
		Entry("empty", ""),
		Entry("traversal", "../../tmp/evil"),
		Entry("absolute", "/abs/evil"),
		Entry("hidden", ".hidden"),
		Entry("forward-slash", "foo/bar"),
	)

	// Load_RejectsUnsafeSessionID pins the symmetric refusal on the
	// read path. An attacker who plants a file under a traversal-
	// escape path must not be able to get SessionMemoryStore to read
	// it by supplying the escape as sessionID.
	DescribeTable("Load refuses unsafe sessionIDs",
		func(id string) {
			dir := GinkgoT().TempDir()
			store := recall.NewSessionMemoryStore(dir)
			err := store.Load(id)
			Expect(err).To(HaveOccurred(),
				"Load(%q) = nil; want refusal", id)
			Expect(errors.Is(err, sessionid.ErrInvalidSessionID)).To(BeTrue(),
				"Load(%q) err = %v; want errors.Is ErrInvalidSessionID", id, err)
		},
		Entry("empty", ""),
		Entry("traversal", "../../tmp/evil"),
		Entry("absolute", "/abs/evil"),
		Entry("hidden", ".hidden"),
		Entry("backslash", "foo\\bar"),
	)

	// Save_AcceptsSafeSessionID belts the positive contract so the
	// validator additions do not regress legitimate use.
	It("Save accepts a safe sessionID and writes the memory file under the matching subdir", func() {
		dir := GinkgoT().TempDir()
		store := recall.NewSessionMemoryStore(dir)
		Expect(store.Save("safe-session-123")).To(Succeed())
		_, err := os.Stat(filepath.Join(dir, "safe-session-123", "memory.json"))
		Expect(err).NotTo(HaveOccurred(),
			"expected memory.json at safe-session-123")
	})
})
