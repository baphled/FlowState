package eventlogger_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEventLogger(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "EventLogger Suite")
}
