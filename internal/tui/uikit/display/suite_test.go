package display_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDisplay(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Display Suite")
}
