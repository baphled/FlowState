package swarm_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSwarm(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Swarm Suite")
}
