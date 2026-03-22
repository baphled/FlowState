package memory_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/memory"
)

var _ = Describe("JSONLStore", func() {
	var (
		tmpDir string
		store  *memory.JSONLStore
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "memory-persistence-*")
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() {
			os.RemoveAll(tmpDir)
		})
	})

	Describe("NewJSONLStore", func() {
		It("creates a store with the given path", func() {
			storePath := filepath.Join(tmpDir, "test.jsonl")
			store = memory.NewJSONLStore(storePath)
			Expect(store).NotTo(BeNil())
		})

		It("uses MEMORY_FILE_PATH env var when path is empty", func() {
			envPath := filepath.Join(tmpDir, "env.jsonl")
			os.Setenv("MEMORY_FILE_PATH", envPath)
			DeferCleanup(func() {
				os.Unsetenv("MEMORY_FILE_PATH")
			})

			store = memory.NewJSONLStore("")
			graph := &memory.KnowledgeGraph{
				Entities: []memory.Entity{
					{Name: "EnvTest", EntityType: "Test", Observations: []string{"from env"}},
				},
			}
			Expect(store.Save(graph)).To(Succeed())

			_, err := os.Stat(envPath)
			Expect(err).NotTo(HaveOccurred())
		})

		It("defaults to memory.jsonl when path and env var are empty", func() {
			os.Unsetenv("MEMORY_FILE_PATH")
			store = memory.NewJSONLStore("")
			Expect(store).NotTo(BeNil())
		})
	})

	Describe("Save and Load", func() {
		BeforeEach(func() {
			storePath := filepath.Join(tmpDir, "test.jsonl")
			store = memory.NewJSONLStore(storePath)
		})

		It("round-trips entities and relations", func() {
			original := &memory.KnowledgeGraph{
				Entities: []memory.Entity{
					{Name: "Alice", EntityType: "Person", Observations: []string{"tall", "friendly"}},
					{Name: "Bob", EntityType: "Person", Observations: []string{"short"}},
				},
				Relations: []memory.Relation{
					{From: "Alice", To: "Bob", RelationType: "knows"},
				},
			}

			Expect(store.Save(original)).To(Succeed())

			loaded, err := store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Entities).To(ConsistOf(original.Entities))
			Expect(loaded.Relations).To(ConsistOf(original.Relations))
		})

		It("preserves empty observations as empty slices", func() {
			original := &memory.KnowledgeGraph{
				Entities: []memory.Entity{
					{Name: "Minimal", EntityType: "Test", Observations: []string{}},
				},
			}

			Expect(store.Save(original)).To(Succeed())

			loaded, err := store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Entities).To(HaveLen(1))
			Expect(loaded.Entities[0].Observations).To(BeEmpty())
		})
	})

	Describe("Load", func() {
		It("returns empty graph when file does not exist", func() {
			storePath := filepath.Join(tmpDir, "nonexistent.jsonl")
			store = memory.NewJSONLStore(storePath)

			loaded, err := store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Entities).To(BeEmpty())
			Expect(loaded.Relations).To(BeEmpty())
		})

		It("returns empty graph for an empty file", func() {
			storePath := filepath.Join(tmpDir, "empty.jsonl")
			Expect(os.WriteFile(storePath, []byte(""), 0o600)).To(Succeed())
			store = memory.NewJSONLStore(storePath)

			loaded, err := store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Entities).To(BeEmpty())
			Expect(loaded.Relations).To(BeEmpty())
		})

		It("skips malformed JSON lines gracefully", func() {
			storePath := filepath.Join(tmpDir, "malformed.jsonl")
			content := `{"type":"entity","name":"Good","entityType":"Test","observations":["ok"]}
this is not valid json
{"type":"relation","from":"A","to":"B","relationType":"knows"}
`
			Expect(os.WriteFile(storePath, []byte(content), 0o600)).To(Succeed())
			store = memory.NewJSONLStore(storePath)

			loaded, err := store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Entities).To(HaveLen(1))
			Expect(loaded.Entities[0].Name).To(Equal("Good"))
			Expect(loaded.Relations).To(HaveLen(1))
			Expect(loaded.Relations[0].From).To(Equal("A"))
		})
	})

	Describe("Save", func() {
		It("uses atomic write pattern with temp file and rename", func() {
			storePath := filepath.Join(tmpDir, "atomic.jsonl")
			store = memory.NewJSONLStore(storePath)

			graph := &memory.KnowledgeGraph{
				Entities: []memory.Entity{
					{Name: "Atomic", EntityType: "Test", Observations: []string{"safe"}},
				},
			}
			Expect(store.Save(graph)).To(Succeed())

			entries, err := os.ReadDir(tmpDir)
			Expect(err).NotTo(HaveOccurred())

			tmpFiles := 0
			for _, entry := range entries {
				if filepath.Ext(entry.Name()) == ".tmp" {
					tmpFiles++
				}
			}
			Expect(tmpFiles).To(Equal(0))

			_, err = os.Stat(storePath)
			Expect(err).NotTo(HaveOccurred())
		})

		It("creates parent directories if they do not exist", func() {
			storePath := filepath.Join(tmpDir, "sub", "dir", "deep.jsonl")
			store = memory.NewJSONLStore(storePath)

			graph := &memory.KnowledgeGraph{
				Entities: []memory.Entity{
					{Name: "Deep", EntityType: "Test", Observations: []string{"nested"}},
				},
			}
			Expect(store.Save(graph)).To(Succeed())

			loaded, err := store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Entities).To(HaveLen(1))
			Expect(loaded.Entities[0].Name).To(Equal("Deep"))
		})

		Context("error paths", func() {
			It("returns error when parent directory cannot be created", func() {
				readOnlyDir := filepath.Join(tmpDir, "readonly")
				Expect(os.MkdirAll(readOnlyDir, 0o555)).To(Succeed())
				DeferCleanup(func() {
					os.Chmod(readOnlyDir, 0o755)
				})

				storePath := filepath.Join(readOnlyDir, "subdir", "memory.jsonl")
				store = memory.NewJSONLStore(storePath)

				graph := &memory.KnowledgeGraph{
					Entities: []memory.Entity{
						{Name: "test", EntityType: "test", Observations: []string{}},
					},
				}
				err := store.Save(graph)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("creating directory"))
			})

			It("returns error when temp file cannot be created", func() {
				storePath := filepath.Join(tmpDir, "nowrite", "memory.jsonl")
				store = memory.NewJSONLStore(storePath)

				Expect(os.MkdirAll(filepath.Dir(storePath), 0o755)).To(Succeed())
				Expect(os.Chmod(filepath.Dir(storePath), 0o555)).To(Succeed())
				DeferCleanup(func() {
					os.Chmod(filepath.Dir(storePath), 0o755)
				})

				graph := &memory.KnowledgeGraph{
					Entities: []memory.Entity{
						{Name: "test", EntityType: "test", Observations: []string{}},
					},
				}
				err := store.Save(graph)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("creating temp file"))
			})

			It("returns error when rename target is an existing directory", func() {
				targetDir := filepath.Join(tmpDir, "rename-fail")
				Expect(os.MkdirAll(targetDir, 0o755)).To(Succeed())

				storePath := filepath.Join(targetDir, "memory.jsonl")
				Expect(os.MkdirAll(storePath, 0o755)).To(Succeed())

				store = memory.NewJSONLStore(storePath)
				graph := &memory.KnowledgeGraph{
					Entities: []memory.Entity{
						{Name: "test", EntityType: "test", Observations: []string{}},
					},
				}
				err := store.Save(graph)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("renaming temp file"))
			})
		})
	})
})
