package read_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/read"
)

var _ = Describe("Read Tool", func() {
	var (
		readTool *read.Tool
		ctx      context.Context
	)

	BeforeEach(func() {
		readTool = read.New()
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns read", func() {
			Expect(readTool.Name()).To(Equal("read"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(readTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("has path in Required", func() {
			schema := readTool.Schema()
			Expect(schema.Required).To(ConsistOf("path"))
		})

		It("exposes path, offset, and limit properties", func() {
			schema := readTool.Schema()
			Expect(schema.Properties).To(HaveKey("path"))
			Expect(schema.Properties).To(HaveKey("offset"))
			Expect(schema.Properties).To(HaveKey("limit"))
		})

		It("documents offset as 1-indexed", func() {
			schema := readTool.Schema()
			offsetProp := schema.Properties["offset"]
			Expect(strings.ToLower(offsetProp.Description)).To(ContainSubstring("1-indexed"))
		})
	})

	Describe("Execute", func() {
		var tempDir string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "read-tool-test-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		Context("when reading an existing file", func() {
			It("returns the file content", func() {
				testPath := filepath.Join(tempDir, "test.txt")
				testContent := "hello world"
				Expect(os.WriteFile(testPath, []byte(testContent), 0o600)).To(Succeed())

				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path": testPath,
					},
				}

				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal(testContent))
			})
		})

		Context("when reading a non-existent file", func() {
			It("returns non-nil Error in result", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path": filepath.Join(tempDir, "nonexistent.txt"),
					},
				}

				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("when path contains traversal", func() {
			It("returns non-nil Error in result", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path": "../etc/passwd",
					},
				}

				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("when path argument is missing", func() {
			It("returns a Go error", func() {
				input := tool.Input{
					Name:      "read",
					Arguments: map[string]interface{}{},
				}

				_, err := readTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when path argument is empty", func() {
			It("returns a Go error", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path": "",
					},
				}

				_, err := readTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("with offset and limit", func() {
			var filePath string

			BeforeEach(func() {
				filePath = filepath.Join(tempDir, "lines.txt")
				lines := make([]string, 100)
				for i := 0; i < 100; i++ {
					lines[i] = "line-" + strings.Repeat("x", 1) + "-" + intToString(i+1)
				}
				Expect(os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0o600)).To(Succeed())
			})

			It("returns the line range [offset, offset+limit) when both are set", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path":   filePath,
						"offset": 10,
						"limit":  3,
					},
				}
				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("line-x-10\nline-x-11\nline-x-12"))
			})

			It("falls back to whole-file read when offset=1 and no limit", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path": filePath,
					},
				}
				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())
				Expect(strings.Count(result.Output, "\n")).To(Equal(99))
			})

			It("returns empty string when offset is past EOF", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path":   filePath,
						"offset": 9999,
						"limit":  10,
					},
				}
				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())
				Expect(result.Output).To(BeEmpty())
			})

			It("rejects offset < 1", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path":   filePath,
						"offset": 0,
					},
				}
				_, err := readTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
			})

			It("accepts offset and limit decoded from JSON as float64", func() {
				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path":   filePath,
						"offset": float64(5),
						"limit":  float64(2),
					},
				}
				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("line-x-5\nline-x-6"))
			})
		})

		Context("interaction with truncation", func() {
			It("returns a small slice cleanly without hitting the truncation cap", func() {
				bigPath := filepath.Join(tempDir, "huge.txt")
				lines := make([]string, 0, 5000)
				for i := 0; i < 5000; i++ {
					lines = append(lines, "row-"+intToString(i+1))
				}
				Expect(os.WriteFile(bigPath, []byte(strings.Join(lines, "\n")), 0o600)).To(Succeed())

				input := tool.Input{
					Name: "read",
					Arguments: map[string]interface{}{
						"path":   bigPath,
						"offset": 500,
						"limit":  10,
					},
				}
				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("row-500"))
				Expect(result.Output).To(ContainSubstring("row-509"))
				Expect(result.Output).NotTo(ContainSubstring("truncated"))
			})

			It("truncates whole-file reads exceeding the line cap", func() {
				bigPath := filepath.Join(tempDir, "overcap.txt")
				lines := make([]string, 0, 3000)
				for i := 0; i < 3000; i++ {
					lines = append(lines, "row-"+intToString(i+1))
				}
				Expect(os.WriteFile(bigPath, []byte(strings.Join(lines, "\n")), 0o600)).To(Succeed())

				input := tool.Input{
					Name:      "read",
					Arguments: map[string]interface{}{"path": bigPath},
				}
				result, err := readTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				// The default constructor has no head-cap, so this
				// oversized unbounded read falls through to the generic
				// truncation envelope (offset hint + grep alternative).
				Expect(result.Output).To(ContainSubstring("truncated"))
				Expect(result.Output).To(ContainSubstring("offset"))
				Expect(result.Output).To(ContainSubstring("grep"))
			})
		})
	})

	Context("head-cap on unbounded reads (NewWithGuardAndLimits)", func() {
		var tempDir string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "read-headcap-test-*")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		It("returns head lines plus trailer for an oversized unbounded read", func() {
			capped := read.NewWithGuardAndLimits(nil, 512, 5)
			bigPath := filepath.Join(tempDir, "oversize.txt")
			lines := make([]string, 0, 300)
			for i := 0; i < 300; i++ {
				lines = append(lines, "row-"+intToString(i+1))
			}
			Expect(os.WriteFile(bigPath, []byte(strings.Join(lines, "\n")), 0o600)).To(Succeed())

			result, err := capped.Execute(ctx, tool.Input{
				Name:      "read",
				Arguments: map[string]interface{}{"path": bigPath},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("row-1\n"))
			Expect(result.Output).To(ContainSubstring("row-5\n"))
			Expect(result.Output).NotTo(ContainSubstring("row-6"))
			Expect(result.Output).To(ContainSubstring("truncated"))
			Expect(result.Output).To(ContainSubstring("300 lines"))
			Expect(result.Output).To(ContainSubstring("offset/limit"))
		})

		It("does not head-cap a bounded read of the same file", func() {
			capped := read.NewWithGuardAndLimits(nil, 512, 5)
			bigPath := filepath.Join(tempDir, "oversize2.txt")
			lines := make([]string, 0, 300)
			for i := 0; i < 300; i++ {
				lines = append(lines, "row-"+intToString(i+1))
			}
			Expect(os.WriteFile(bigPath, []byte(strings.Join(lines, "\n")), 0o600)).To(Succeed())

			result, err := capped.Execute(ctx, tool.Input{
				Name: "read",
				Arguments: map[string]interface{}{
					"path":   bigPath,
					"offset": 10,
					"limit":  2,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(Equal("row-10\nrow-11"))
		})

		It("does not head-cap small files", func() {
			capped := read.NewWithGuardAndLimits(nil, 10240, 200)
			smallPath := filepath.Join(tempDir, "small.txt")
			Expect(os.WriteFile(smallPath, []byte("tiny"), 0o600)).To(Succeed())

			result, err := capped.Execute(ctx, tool.Input{
				Name:      "read",
				Arguments: map[string]interface{}{"path": smallPath},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(Equal("tiny"))
		})

		It("defaults threshold/head lines to 10240/200 on zero values", func() {
			capped := read.NewWithGuardAndLimits(nil, 0, 0)
			// 200 lines × 100 bytes = 20000 bytes > 10240 default threshold.
			bigPath := filepath.Join(tempDir, "defaultcap.txt")
			lines := make([]string, 0, 200)
			for i := 0; i < 200; i++ {
				lines = append(lines, strings.Repeat("y", 99)+"-"+intToString(i+1))
			}
			Expect(os.WriteFile(bigPath, []byte(strings.Join(lines, "\n")), 0o600)).To(Succeed())

			result, err := capped.Execute(ctx, tool.Input{
				Name:      "read",
				Arguments: map[string]interface{}{"path": bigPath},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("truncated"))
			Expect(result.Output).To(ContainSubstring("200 lines"))
		})

		It("plain New stays unbounded for small files and head-caps nothing below threshold", func() {
			// read.New() has no head-cap configured; the existing
			// truncation envelope path remains its only bound.
			plain := read.New()
			smallPath := filepath.Join(tempDir, "plain.txt")
			Expect(os.WriteFile(smallPath, []byte("ok"), 0o600)).To(Succeed())
			result, err := plain.Execute(ctx, tool.Input{
				Name:      "read",
				Arguments: map[string]interface{}{"path": smallPath},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(Equal("ok"))
		})
	})
})

// intToString avoids strconv import noise in the spec body.
func intToString(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
