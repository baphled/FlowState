package atomicwrite_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAtomicWrite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AtomicWrite Suite")
}
