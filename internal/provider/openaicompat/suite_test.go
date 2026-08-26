package openaicompat_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestOpenAICompat(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OpenAI Compat Suite")
}
