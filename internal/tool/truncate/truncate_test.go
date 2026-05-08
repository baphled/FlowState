package truncate_test

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool/truncate"
)

var _ = Describe("Tool Output Truncation", func() {
	var (
		tmpDir string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "truncate-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir)
	})

	Describe("Under-cap passthrough", func() {
		It("returns the original text unchanged when under both budgets", func() {
			text := "small payload\nfits easily"
			result := truncate.Apply(text, truncate.Options{Dir: tmpDir, SessionID: "sess-1"})
			Expect(result.Truncated).To(BeFalse())
			Expect(result.Content).To(Equal(text))
			Expect(result.OutputPath).To(BeEmpty())
		})

		It("does not write a spill file for under-cap input", func() {
			text := "tiny"
			_ = truncate.Apply(text, truncate.Options{Dir: tmpDir, SessionID: "sess-2"})
			entries, _ := os.ReadDir(tmpDir)
			Expect(entries).To(BeEmpty())
		})
	})

	Describe("Byte cap", func() {
		It("truncates content exceeding the byte cap and reports truncated=true", func() {
			big := strings.Repeat("x", 60*1024) // 60KB single line
			result := truncate.Apply(big, truncate.Options{Dir: tmpDir, SessionID: "sess-bytes"})
			Expect(result.Truncated).To(BeTrue())
			Expect(len(result.Content)).To(BeNumerically("<", len(big)))
			Expect(result.OutputPath).NotTo(BeEmpty())
		})

		It("writes the FULL original content to the overflow path", func() {
			big := strings.Repeat("a", 60*1024)
			result := truncate.Apply(big, truncate.Options{Dir: tmpDir, SessionID: "sess-overflow"})
			data, err := os.ReadFile(result.OutputPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(Equal(big))
		})
	})

	Describe("Line cap", func() {
		It("truncates content exceeding the line cap when bytes are within budget", func() {
			lines := make([]string, 0, 3000)
			for i := 0; i < 3000; i++ {
				lines = append(lines, "ok")
			}
			text := strings.Join(lines, "\n")
			result := truncate.Apply(text, truncate.Options{Dir: tmpDir, SessionID: "sess-lines"})
			Expect(result.Truncated).To(BeTrue())
			// Content should be smaller than original by at least the missing line count.
			Expect(strings.Count(result.Content, "\n")).To(BeNumerically("<", 3000))
		})
	})

	Describe("Hint format", func() {
		It("embeds a recovery hint mentioning grep, read with offset/limit, and the overflow path", func() {
			big := strings.Repeat("y", 60*1024)
			result := truncate.Apply(big, truncate.Options{Dir: tmpDir, SessionID: "sess-hint"})
			Expect(result.Content).To(ContainSubstring("grep"))
			Expect(result.Content).To(ContainSubstring("offset"))
			Expect(result.Content).To(ContainSubstring("limit"))
			Expect(result.Content).To(ContainSubstring(result.OutputPath))
		})

		It("includes a removal-summary fragment indicating cap kind", func() {
			big := strings.Repeat("z", 60*1024)
			result := truncate.Apply(big, truncate.Options{Dir: tmpDir, SessionID: "sess-cap"})
			Expect(result.Content).To(ContainSubstring("truncated"))
		})
	})

	Describe("Direction", func() {
		It("keeps the head slice by default", func() {
			lines := make([]string, 0, 3000)
			for i := 0; i < 3000; i++ {
				lines = append(lines, "L")
			}
			lines[0] = "FIRST_MARKER"
			lines[2999] = "LAST_MARKER"
			text := strings.Join(lines, "\n")
			result := truncate.Apply(text, truncate.Options{Dir: tmpDir, SessionID: "sess-head"})
			Expect(result.Truncated).To(BeTrue())
			Expect(result.Content).To(ContainSubstring("FIRST_MARKER"))
			Expect(result.Content).NotTo(ContainSubstring("LAST_MARKER"))
		})

		It("keeps the tail slice when Direction=Tail", func() {
			lines := make([]string, 0, 3000)
			for i := 0; i < 3000; i++ {
				lines = append(lines, "L")
			}
			lines[0] = "FIRST_MARKER"
			lines[2999] = "LAST_MARKER"
			text := strings.Join(lines, "\n")
			result := truncate.Apply(text, truncate.Options{
				Dir:       tmpDir,
				SessionID: "sess-tail",
				Direction: truncate.Tail,
			})
			Expect(result.Truncated).To(BeTrue())
			Expect(result.Content).To(ContainSubstring("LAST_MARKER"))
			Expect(result.Content).NotTo(ContainSubstring("FIRST_MARKER"))
		})
	})

	Describe("Overflow path scoping", func() {
		It("scopes the spill file under <dir>/<session>/", func() {
			big := strings.Repeat("p", 60*1024)
			result := truncate.Apply(big, truncate.Options{
				Dir:       tmpDir,
				SessionID: "scoped-session-abc",
				ToolName:  "bash",
			})
			Expect(result.OutputPath).To(HavePrefix(filepath.Join(tmpDir, "scoped-session-abc")))
		})

		It("falls back to _unscoped when SessionID is empty", func() {
			big := strings.Repeat("q", 60*1024)
			result := truncate.Apply(big, truncate.Options{Dir: tmpDir, ToolName: "ls"})
			Expect(result.OutputPath).To(HavePrefix(filepath.Join(tmpDir, "_unscoped")))
		})

		It("includes the tool name in the spill filename", func() {
			big := strings.Repeat("r", 60*1024)
			result := truncate.Apply(big, truncate.Options{
				Dir:       tmpDir,
				SessionID: "sess-name",
				ToolName:  "grep",
			})
			Expect(filepath.Base(result.OutputPath)).To(HavePrefix("grep-"))
		})

		It("sanitises session IDs to prevent path traversal", func() {
			big := strings.Repeat("s", 60*1024)
			result := truncate.Apply(big, truncate.Options{
				Dir:       tmpDir,
				SessionID: "../etc",
			})
			Expect(result.OutputPath).NotTo(ContainSubstring("/etc/"))
			Expect(result.OutputPath).To(HavePrefix(tmpDir))
		})
	})

	Describe("Per-call overrides", func() {
		It("respects MaxBytes override (tighter than default)", func() {
			text := strings.Repeat("a", 2048) // 2KB single line
			result := truncate.Apply(text, truncate.Options{
				Dir:       tmpDir,
				SessionID: "sess-tight",
				MaxBytes:  1024,
			})
			Expect(result.Truncated).To(BeTrue())
		})

		It("respects MaxLines override", func() {
			lines := make([]string, 100)
			for i := range lines {
				lines[i] = "x"
			}
			text := strings.Join(lines, "\n")
			result := truncate.Apply(text, truncate.Options{
				Dir:       tmpDir,
				SessionID: "sess-tight-lines",
				MaxLines:  50,
			})
			Expect(result.Truncated).To(BeTrue())
		})
	})
})
