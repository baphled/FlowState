package multiedit_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMultiEdit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MultiEdit Tool Suite")
}
