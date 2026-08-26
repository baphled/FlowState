package streaming_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStreaming(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Streaming Suite")
}
