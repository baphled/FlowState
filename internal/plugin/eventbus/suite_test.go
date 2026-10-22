package eventbus

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEventBus(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "EventBus Suite")
}
