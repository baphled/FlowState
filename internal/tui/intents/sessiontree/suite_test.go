package sessiontree_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSessiontree(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sessiontree Suite")
}
