package learning_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestLearning(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Learning Suite")
}
