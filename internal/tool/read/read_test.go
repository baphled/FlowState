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
				Expect(result.Output).To(ContainSubstring("truncated"))
				Expect(result.Output).To(ContainSubstring("offset"))
				Expect(result.Output).To(ContainSubstring("grep"))
			})
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
