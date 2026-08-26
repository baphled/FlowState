package coordination_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCoordinationTool(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Coordination Tool Suite")
}
