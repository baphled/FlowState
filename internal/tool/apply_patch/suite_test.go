package applypatch_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestApplyPatch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Apply Patch Suite")
}
