package main_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

func TestMain(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Memory Server Suite")
}

var _ = Describe("flowstate-memory-server", func() {
	It("compiles to a binary", func() {
		compiledPath, err := gexec.Build("github.com/baphled/flowstate/cmd/flowstate-memory-server")
		Expect(err).NotTo(HaveOccurred())
		Expect(compiledPath).NotTo(BeEmpty())
		gexec.CleanupBuildArtifacts()
	})
})
