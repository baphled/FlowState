package coordination_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
)

var _ = Describe("Store", func() {
	var (
		store    coordination.Store
		tempDir  string
		filePath string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "coordination-test-*")
		Expect(err).NotTo(HaveOccurred())
		filePath = filepath.Join(tempDir, "coordination.json")
		store = coordination.NewFileStore(filePath)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("Set and Get", func() {
		It("stores and retrieves a value", func() {
			err := store.Set("test-key", []byte("test-value"))
			Expect(err).NotTo(HaveOccurred())

			value, err := store.Get("test-key")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(value)).To(Equal("test-value"))
		})

		It("creates the file on first write", func() {
			Expect(filePath).NotTo(BeAnExistingFile())

			err := store.Set("key", []byte("value"))
			Expect(err).NotTo(HaveOccurred())
			Expect(filePath).To(BeAnExistingFile())
		})

		It("overwrites an existing key", func() {
			err := store.Set("key", []byte("value1"))
			Expect(err).NotTo(HaveOccurred())

			err = store.Set("key", []byte("value2"))
			Expect(err).NotTo(HaveOccurred())

			value, err := store.Get("key")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(value)).To(Equal("value2"))
		})

		It("returns an error for non-existent key", func() {
			_, err := store.Get("nonexistent")
			Expect(err).To(MatchError("key not found: nonexistent"))
		})
	})

	Describe("List", func() {
		BeforeEach(func() {
			entries := map[string]string{
				"chain1/requirements": "user requirements",
				"chain1/interview":    "interview transcript",
				"chain1/plan":         "generated plan",
				"chain2/requirements": "other requirements",
				"shared/key":          "shared value",
			}

			for k, v := range entries {
				err := store.Set(k, []byte(v))
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("lists all keys with empty prefix", func() {
			keys, err := store.List("")
			Expect(err).NotTo(HaveOccurred())
			Expect(keys).To(HaveLen(5))
		})

		It("lists keys with chain prefix", func() {
			keys, err := store.List("chain1/")
			Expect(err).NotTo(HaveOccurred())
			Expect(keys).To(HaveLen(3))
			Expect(keys).To(ContainElements(
				"chain1/requirements",
				"chain1/interview",
				"chain1/plan",
			))
		})

		It("lists keys with full key match", func() {
			keys, err := store.List("chain1/requirements")
			Expect(err).NotTo(HaveOccurred())
			Expect(keys).To(HaveLen(1))
			Expect(keys[0]).To(Equal("chain1/requirements"))
		})

		It("returns empty slice for non-matching prefix", func() {
			keys, err := store.List("nonexistent/")
			Expect(err).NotTo(HaveOccurred())
			Expect(keys).To(BeEmpty())
		})

		It("returns empty slice for empty store", func() {
			emptyStore := coordination.NewFileStore(filepath.Join(tempDir, "empty.json"))
			keys, err := emptyStore.List("")
			Expect(err).NotTo(HaveOccurred())
			Expect(keys).To(BeEmpty())
		})
	})

	Describe("Delete", func() {
		It("deletes an existing key", func() {
			err := store.Set("to-delete", []byte("value"))
			Expect(err).NotTo(HaveOccurred())

			err = store.Delete("to-delete")
			Expect(err).NotTo(HaveOccurred())

			_, err = store.Get("to-delete")
			Expect(err).To(MatchError("key not found: to-delete"))
		})

		It("returns error for non-existent key", func() {
			err := store.Delete("nonexistent")
			Expect(err).To(MatchError("key not found: nonexistent"))
		})
	})

	Describe("concurrent access", func() {
		It("handles concurrent reads and writes", func() {
			var (
				numWriters = 10
				numReads   = 10
				iterations = 20
			)

			done := make(chan bool, numWriters+numReads)

			// Writers
			for range numWriters {
				go func() {
					defer GinkgoRecover()
					for range iterations {
						key := "concurrent/key"
						value := []byte("value-writer-value")
						err := store.Set(key, value)
						Expect(err).NotTo(HaveOccurred())
					}
					done <- true
				}()
			}

			// Readers
			for range numReads {
				go func() {
					defer GinkgoRecover()
					for range iterations {
						_, err := store.Get("concurrent/key")
						Expect(err).NotTo(HaveOccurred())
					}
					done <- true
				}()
			}

			// Wait for all goroutines
			for range numWriters + numReads {
				<-done
			}
		})
	})
})

var _ = Describe("FileStore XDG Path", func() {
	It("uses default XDG data directory path", func() {
		store := coordination.NewFileStore("")
		Expect(store).NotTo(BeNil())
	})
})
