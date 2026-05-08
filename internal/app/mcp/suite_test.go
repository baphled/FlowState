package mcp_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAppMCP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "App MCP Wire Suite")
}
