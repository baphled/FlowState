package delegation_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDelegation(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Delegation Suite")
}
