package pathguard_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool/pathguard"
)

var _ = Describe("ResolvePath", func() {
	DescribeTable("the shared file-tool path policy",
		func(input string, expectErr bool, expectPath string) {
			got, err := pathguard.ResolvePath(input)
			if expectErr {
				Expect(err).To(MatchError(pathguard.ErrPathTraversal))
				Expect(err.Error()).To(ContainSubstring("path traversal"),
					"the error message must stay stable so every tool reads identically")
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(expectPath))
		},
		Entry("accepts an absolute path outside the working directory",
			"/home/user/.config/flowstate/agents/x.md", false, "/home/user/.config/flowstate/agents/x.md"),
		Entry("accepts a relative path inside the working directory",
			"internal/tool/edit/edit.go", false, "internal/tool/edit/edit.go"),
		Entry("collapses dot segments",
			"./foo/./bar.txt", false, "foo/bar.txt"),
		Entry("trims surrounding whitespace",
			"  /abs/path.txt  ", false, "/abs/path.txt"),
		Entry("rejects a leading parent traversal",
			"../outside.txt", true, ""),
		Entry("rejects a trailing parent traversal that escapes the root",
			"foo/../../etc/passwd", true, ""),
		Entry("rejects an embedded parent traversal that escapes the root",
			"a/../..", true, ""),
	)
})
