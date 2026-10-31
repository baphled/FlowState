package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"testing"
)

func TestFlowstateCmd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FlowState CLI Suite")
}

// specDB holds the per-spec database path shared by chained CLI
// invocations inside a single It.
var specDB string

var _ = BeforeEach(func() {
	specDB = ""
})

// dbFor returns the per-spec database path, creating it once.
func dbFor() string {
	if specDB != "" {
		return specDB
	}
	specDB = GinkgoT().TempDir() + "/test.db"
	return specDB
}
