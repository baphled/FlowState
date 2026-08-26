package ollamacloud_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOllamaCloud(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OllamaCloud Provider Suite")
}
