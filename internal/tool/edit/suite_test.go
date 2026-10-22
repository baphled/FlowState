package edit_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEdit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Edit Suite")
}
