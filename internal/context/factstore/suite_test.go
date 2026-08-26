package factstore_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFactstore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Factstore Suite")
}
