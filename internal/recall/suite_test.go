package recall_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRecall(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Recall Suite")
}
