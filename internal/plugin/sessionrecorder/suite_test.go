package sessionrecorder_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSessionRecorder(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SessionRecorder Suite")
}
