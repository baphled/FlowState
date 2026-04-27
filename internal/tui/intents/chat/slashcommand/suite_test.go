package slashcommand_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSlashCommand(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SlashCommand Suite")
}
