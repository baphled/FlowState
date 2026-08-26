package truncate_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	// --- Slice 3: spill-file cleanup scheduler ----------------------
	//
	// Phase 4 spilled oversized tool outputs to <root>/<session>/. With
	// no retention the directory grows unbounded. These specs pin the
	// hourly-tick / 7-day-retention shape OpenCode's
	// tool/truncation.ts:25-42 ships, scoped to FlowState's truncate
	// package layout.

	Describe("Cleanup", func() {
		writeAged := func(path string, age time.Duration) {
			GinkgoHelper()
			Expect(os.MkdirAll(filepath.Dir(path), 0o700)).To(Succeed())
			Expect(os.WriteFile(path, []byte("payload"), 0o600)).To(Succeed())
			when := time.Now().Add(-age)
			Expect(os.Chtimes(path, when, when)).To(Succeed())
		}

		It("removes files older than retention but keeps fresh files", func() {
			old := filepath.Join(tmpDir, "old.txt")
			mid := filepath.Join(tmpDir, "mid.txt")
			fresh := filepath.Join(tmpDir, "fresh.txt")
			writeAged(old, 8*24*time.Hour)
			writeAged(mid, 3*24*time.Hour)
			writeAged(fresh, 1*time.Minute)

			Expect(truncate.Cleanup(tmpDir, 7*24*time.Hour)).To(Succeed())

			Expect(old).NotTo(BeAnExistingFile())
			Expect(mid).To(BeAnExistingFile())
			Expect(fresh).To(BeAnExistingFile())
		})

		It("walks session subdirectories and prunes their stale files", func() {
			sessFile := filepath.Join(tmpDir, "session-a", "stale.txt")
			writeAged(sessFile, 8*24*time.Hour)

			Expect(truncate.Cleanup(tmpDir, 7*24*time.Hour)).To(Succeed())

			Expect(sessFile).NotTo(BeAnExistingFile())
			// Session dir itself can survive empty — Cleanup is per-file.
			Expect(filepath.Dir(sessFile)).To(BeADirectory())
		})

		It("returns nil when root does not exist", func() {
			missing := filepath.Join(tmpDir, "does-not-exist")
			Expect(truncate.Cleanup(missing, 7*24*time.Hour)).To(Succeed())
		})

		It("skips files newer than the mid-write safety window", func() {
			// Files with mtime in the future or under the 5-second
			// safety window must survive even when retention is 0,
			// because Cleanup must not race a concurrent spill write.
			active := filepath.Join(tmpDir, "active.txt")
			Expect(os.WriteFile(active, []byte("in flight"), 0o600)).To(Succeed())

			Expect(truncate.Cleanup(tmpDir, 0)).To(Succeed())

			Expect(active).To(BeAnExistingFile())
		})
	})

	Describe("StartCleanupScheduler", func() {
		It("ticks Cleanup at the configured interval", func() {
			old := filepath.Join(tmpDir, "old.txt")
			Expect(os.WriteFile(old, []byte("x"), 0o600)).To(Succeed())
			when := time.Now().Add(-8 * 24 * time.Hour)
			Expect(os.Chtimes(old, when, when)).To(Succeed())

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stop := truncate.StartCleanupScheduler(
				ctx,
				tmpDir,
				7*24*time.Hour,
				20*time.Millisecond,
			)
			defer stop()

			// First sweep is invoked synchronously at scheduler start
			// so the file should disappear well within a single tick.
			Eventually(func() bool {
				_, err := os.Stat(old)
				return os.IsNotExist(err)
			}, 500*time.Millisecond, 10*time.Millisecond).Should(BeTrue())
		})

		It("is idempotent on stop", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stop := truncate.StartCleanupScheduler(
				ctx,
				tmpDir,
				7*24*time.Hour,
				50*time.Millisecond,
			)
			Expect(func() {
				stop()
				stop()
			}).NotTo(Panic())
		})
	})
})
