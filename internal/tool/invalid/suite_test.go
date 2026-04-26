package invalid_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestInvalid(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Invalid Tool Suite")
}
