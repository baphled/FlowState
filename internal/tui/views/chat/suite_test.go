package chat_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestChatView(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Chat View Suite")
}
