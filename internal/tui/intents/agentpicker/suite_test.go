package agentpicker_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAgentpicker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agentpicker Suite")
}
