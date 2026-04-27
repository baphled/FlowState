package gates_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGates(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Gates Suite")
}
