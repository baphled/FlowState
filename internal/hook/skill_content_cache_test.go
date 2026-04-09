package hook_test

import (
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/hook"
)

type skillFile struct {
	name    string
	content string
}

var _ = Describe("SkillContentCache", func() {
	var (
		tempDir string
		skills  []skillFile
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "skillcachetest")
		Expect(err).NotTo(HaveOccurred())
		skills = []skillFile{
			{"alpha", "---\ntitle: Alpha\n---\nAlpha content"},
			{"beta", "Beta content"},
			{"gamma", "---\ntitle: Gamma\n---\nGamma content\n"},
		}
		for _, s := range skills {
			dir := filepath.Join(tempDir, s.name)
			os.MkdirAll(dir, 0o755)
			os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(s.content), 0o600)
		}
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	It("initialises and loads all skill contents, stripping frontmatter", func() {
		cache := hook.NewSkillContentCache(tempDir)
		err := cache.Init()
		Expect(err).NotTo(HaveOccurred())
		for _, s := range skills {
			content, ok := cache.GetContent(s.name)
			Expect(ok).To(BeTrue(), s.name)
			Expect(content).To(ContainSubstring("content"))
			Expect(content).NotTo(ContainSubstring("title:"))
		}
	})

	It("returns false for missing skill", func() {
		cache := hook.NewSkillContentCache(tempDir)
		cache.Init()
		_, ok := cache.GetContent("missing")
		Expect(ok).To(BeFalse())
	})

	It("returns all skill names", func() {
		cache := hook.NewSkillContentCache(tempDir)
		cache.Init()
		names := cache.AllNames()
		Expect(names).To(ConsistOf("alpha", "beta", "gamma"))
	})

	It("tracks byte size per skill and in total", func() {
		cache := hook.NewSkillContentCache(tempDir)
		cache.Init()
		total := 0
		for _, s := range skills {
			sz := cache.ByteSize(s.name)
			Expect(sz).To(BeNumerically(">", 0))
			total += sz
		}
		Expect(cache.TotalBytes()).To(Equal(total))
	})

	It("is safe for concurrent access", func() {
		cache := hook.NewSkillContentCache(tempDir)
		cache.Init()
		wg := sync.WaitGroup{}
		for range [10]int{} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, s := range skills {
					cache.GetContent(s.name)
					cache.ByteSize(s.name)
					cache.HasSkill(s.name)
				}
				cache.AllNames()
				cache.TotalBytes()
			}()
		}
		wg.Wait()
	})

	It("handles empty or missing skill directory gracefully", func() {
		emptyDir, _ := os.MkdirTemp("", "emptycache")
		cache := hook.NewSkillContentCache(emptyDir)
		err := cache.Init()
		Expect(err).NotTo(HaveOccurred())
		Expect(cache.AllNames()).To(BeEmpty())
		os.RemoveAll(emptyDir)
	})
})
