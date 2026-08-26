package sessionid_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSessionID(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SessionID Suite")
}
