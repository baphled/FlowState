package swarmactivity_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSwarmActivity(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SwarmActivity Suite")
}

var _ = Describe("SwarmActivity", func() {
	It("compiles", func() {
		Expect(true).To(BeTrue())
	})
})
