package sessionviewer_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSessionviewer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sessionviewer Suite")
}
