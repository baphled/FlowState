package mcpproxy_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMCPProxy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MCP Proxy Suite")
}
