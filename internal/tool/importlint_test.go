package tool

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Principal architecture condition: internal/tool must stay a leaf
// port — no internal/app or cmd/* imports in production files.
var _ = Describe("import lint", func() {
	It("internal/tool imports neither internal/app nor cmd packages", func() {
		dir, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		checked := 0
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
			Expect(perr).NotTo(HaveOccurred())
			checked++
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				Expect(path).NotTo(HavePrefix("github.com/baphled/flowstate/internal/app"), "%s imports internal/app", e.Name())
				Expect(path).NotTo(HavePrefix("github.com/baphled/flowstate/cmd"), "%s imports cmd/*", e.Name())
			}
		}
		Expect(checked).To(BeNumerically(">", 0))
	})
})
