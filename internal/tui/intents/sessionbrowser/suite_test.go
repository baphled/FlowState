package sessionbrowser_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSessionbrowser(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sessionbrowser Suite")
}
