package coordination_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/coordination"
)

var _ = Describe("FileStore", func() {
	var (
		store *coordination.FileStore
		dir   string
		path  string
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		path = filepath.Join(dir, "coordination.json")
	})

	Describe("NewFileStore", func() {
		Context("when the file does not exist", func() {
			It("creates an empty store", func() {
				var err error
				store, err = coordination.NewFileStore(path)
				Expect(err).NotTo(HaveOccurred())
				Expect(store).NotTo(BeNil())

				keys, err := store.List("")
				Expect(err).NotTo(HaveOccurred())
				Expect(keys).To(BeEmpty())
			})
		})

		Context("when the directory does not exist", func() {
			It("creates the directory and an empty store", func() {
				nestedPath := filepath.Join(dir, "nested", "deep", "coordination.json")
				var err error
				store, err = coordination.NewFileStore(nestedPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(store).NotTo(BeNil())
			})
		})

		Context("when the file contains invalid JSON", func() {
			It("returns an error", func() {
				Expect(os.WriteFile(path, []byte("not json"), 0o600)).To(Succeed())

				_, err := coordination.NewFileStore(path)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("parse coordination store"))
			})
		})
	})

	Describe("Set and Get", func() {
		BeforeEach(func() {
			var err error
			store, err = coordination.NewFileStore(path)
			Expect(err).NotTo(HaveOccurred())
		})

		It("round-trips data correctly", func() {
			err := store.Set("mykey", []byte("myvalue"))
			Expect(err).NotTo(HaveOccurred())

			val, err := store.Get("mykey")
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal([]byte("myvalue")))
		})

		Context("when the key does not exist", func() {
			It("returns ErrKeyNotFound", func() {
				_, err := store.Get("nonexistent")
				Expect(err).To(MatchError(coordination.ErrKeyNotFound))
			})
		})
	})

	Describe("persistence across instances", func() {
		It("data survives creating a new FileStore from the same path", func() {
			var err error
			store, err = coordination.NewFileStore(path)
			Expect(err).NotTo(HaveOccurred())

			Expect(store.Set("persist/key1", []byte("value1"))).To(Succeed())
			Expect(store.Set("persist/key2", []byte("value2"))).To(Succeed())

			// Create a brand new store from the same file.
			store2, err := coordination.NewFileStore(path)
			Expect(err).NotTo(HaveOccurred())

			val1, err := store2.Get("persist/key1")
			Expect(err).NotTo(HaveOccurred())
			Expect(val1).To(Equal([]byte("value1")))

			val2, err := store2.Get("persist/key2")
			Expect(err).NotTo(HaveOccurred())
			Expect(val2).To(Equal([]byte("value2")))
		})
	})

	Describe("Delete", func() {
		BeforeEach(func() {
			var err error
			store, err = coordination.NewFileStore(path)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when the key exists", func() {
			It("removes the key and persists the deletion", func() {
				Expect(store.Set("todelete", []byte("val"))).To(Succeed())

				err := store.Delete("todelete")
				Expect(err).NotTo(HaveOccurred())

				_, err = store.Get("todelete")
				Expect(err).To(MatchError(coordination.ErrKeyNotFound))

				// Verify deletion persisted.
				store2, err := coordination.NewFileStore(path)
				Expect(err).NotTo(HaveOccurred())

				_, err = store2.Get("todelete")
				Expect(err).To(MatchError(coordination.ErrKeyNotFound))
			})
		})

		Context("when the key does not exist", func() {
			It("returns ErrKeyNotFound", func() {
				err := store.Delete("nonexistent")
				Expect(err).To(MatchError(coordination.ErrKeyNotFound))
			})
		})
	})

	Describe("Increment", func() {
		BeforeEach(func() {
			var err error
			store, err = coordination.NewFileStore(path)
			Expect(err).NotTo(HaveOccurred())
		})

		It("creates the counter at 1 when key does not exist", func() {
			val, err := store.Increment("counter")
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(1))
		})

		It("increments an existing counter", func() {
			_, err := store.Increment("counter")
			Expect(err).NotTo(HaveOccurred())

			val, err := store.Increment("counter")
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(2))
		})

		It("persists the incremented value", func() {
			_, err := store.Increment("counter")
			Expect(err).NotTo(HaveOccurred())
			_, err = store.Increment("counter")
			Expect(err).NotTo(HaveOccurred())

			store2, err := coordination.NewFileStore(path)
			Expect(err).NotTo(HaveOccurred())

			val, err := store2.Increment("counter")
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(3))
		})
	})

	Describe("List", func() {
		BeforeEach(func() {
			var err error
			store, err = coordination.NewFileStore(path)
			Expect(err).NotTo(HaveOccurred())

			Expect(store.Set("chainA/plan", []byte("plan-a"))).To(Succeed())
			Expect(store.Set("chainA/review", []byte("review-a"))).To(Succeed())
			Expect(store.Set("chainB/plan", []byte("plan-b"))).To(Succeed())
		})

		Context("when keys match the prefix", func() {
			It("returns matching keys", func() {
				keys, err := store.List("chainA/")
				Expect(err).NotTo(HaveOccurred())
				Expect(keys).To(ConsistOf("chainA/plan", "chainA/review"))
			})
		})

		Context("when no keys match the prefix", func() {
			It("returns an empty slice", func() {
				keys, err := store.List("chainC/")
				Expect(err).NotTo(HaveOccurred())
				Expect(keys).To(BeEmpty())
			})
		})
	})

	Describe("Store interface compliance", func() {
		It("satisfies the Store interface", func() {
			var err error
			store, err = coordination.NewFileStore(path)
			Expect(err).NotTo(HaveOccurred())

			// Compile-time check via assignment.
			var _ coordination.Store = store
		})
	})
})
