package truncate_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTruncate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tool Truncate Suite")
}
