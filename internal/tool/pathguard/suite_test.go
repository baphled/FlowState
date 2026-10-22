package pathguard_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPathguard(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Pathguard Suite")
}
