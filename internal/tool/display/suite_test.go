package tooldisplay_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestToolDisplay(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ToolDisplay Suite")
}
