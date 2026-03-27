package recall_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRecallTools(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Recall Tools Suite")
}
