package ls_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLS(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LS Suite")
}
