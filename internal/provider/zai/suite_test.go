package zai_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestZAI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ZAI Provider Suite")
}
