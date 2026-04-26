package all_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAll(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Plugin Builtin All Suite")
}

// internal/plugin/builtin/all is a barrel package: it imports every
// builtin plugin so that an `import _ "…/all"` pulls them all in for
// registration via init(). The original test file existed only to make
// `go test` compile and run the package's init blocks; we preserve that
// behaviour here.
var _ = Describe("Plugin builtin all barrel", func() {
	It("compiles and registers with no observable failure", func() {
		// Empty by design — package-level init() side effects are the
		// thing under test.
		Expect(true).To(BeTrue())
	})
})
