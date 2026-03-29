package external_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestExternal(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "External Plugin Suite")
}
