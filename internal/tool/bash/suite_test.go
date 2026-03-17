package bash_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBash(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bash Tool Suite")
}
