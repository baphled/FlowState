package applypatch_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	applypatch "github.com/baphled/flowstate/internal/tool/apply_patch"
	"github.com/baphled/flowstate/internal/tool/toolset"
)

var _ = Describe("ApplyPatch tool", func() {
	var (
		tmpDir       string
		toolInstance *applypatch.Tool
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp(".", "apply-patch-test-*")
		Expect(err).NotTo(HaveOccurred())
		toolInstance = applypatch.New()
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	It("applies an inline patch to a file", func() {
		filePath := filepath.Join(tmpDir, "example.txt")
		Expect(os.WriteFile(filePath, []byte("hello\nworld\n"), 0o600)).To(Succeed())

		result, err := toolInstance.Execute(context.Background(), tool.Input{
			Arguments: map[string]interface{}{
				"patch": "*** Begin Patch\n*** Update File: " + filePath + "\n@@\n-hello\n+goodbye\n*** End Patch\n",
			},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Error).NotTo(HaveOccurred())
		updated, readErr := os.ReadFile(filePath)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(string(updated)).To(Equal("goodbye\nworld\n"))
	})

	It("applies a patch loaded from a file", func() {
		filePath := filepath.Join(tmpDir, "note.txt")
		patchPath := filepath.Join(tmpDir, "change.patch")
		Expect(os.WriteFile(filePath, []byte("one\n"), 0o600)).To(Succeed())
		Expect(os.WriteFile(patchPath, []byte("*** Begin Patch\n*** Update File: "+filePath+"\n@@\n-one\n+two\n*** End Patch\n"), 0o600)).To(Succeed())

		result, err := toolInstance.Execute(context.Background(), tool.Input{
			Arguments: map[string]interface{}{"patch": patchPath},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Error).NotTo(HaveOccurred())
		updated, readErr := os.ReadFile(filePath)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(string(updated)).To(Equal("two\n"))
	})

	It("returns a conflict when the hunk does not match", func() {
		filePath := filepath.Join(tmpDir, "conflict.txt")
		Expect(os.WriteFile(filePath, []byte("alpha\n"), 0o600)).To(Succeed())

		result, err := toolInstance.Execute(context.Background(), tool.Input{
			Arguments: map[string]interface{}{
				"patch": "*** Begin Patch\n*** Update File: " + filePath + "\n@@\n-beta\n+gamma\n*** End Patch\n",
			},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Error).To(HaveOccurred())
		Expect(result.Error.Error()).To(ContainSubstring("conflict"))
	})

	It("rejects path traversal targets", func() {
		result, err := toolInstance.Execute(context.Background(), tool.Input{
			Arguments: map[string]interface{}{
				"patch": "*** Begin Patch\n*** Update File: ../outside.txt\n@@\n-one\n+two\n*** End Patch\n",
			},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Error).To(HaveOccurred())
		Expect(result.Error.Error()).To(ContainSubstring("path traversal"))
	})

	It("registers in the default toolset", func() {
		registry := toolset.NewDefaultRegistry("test-key", "")
		toolRegistered, err := registry.Get("apply_patch")
		Expect(err).NotTo(HaveOccurred())
		Expect(toolRegistered.Name()).To(Equal("apply_patch"))
	})
})
