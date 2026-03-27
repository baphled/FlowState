package toast_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestToastExpiry(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Toast Suite")
}
