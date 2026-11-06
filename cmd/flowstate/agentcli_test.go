package main

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// agentCLI doubles the provider resolver so specs exercise the CLI
// routing without a live provider.
var _ = Describe("session agent CLI", func() {
	var tmp string
	BeforeEach(func() { tmp = GinkgoT().TempDir() })

	It("rejects a missing session id with usage exit 2", func() {
		code := cmdAgent(context.Background(), filepath.Join(tmp, "db.sqlite"), &flags{db: filepath.Join(tmp, "db.sqlite")}, "", os.Stdout, os.Stderr)
		Expect(code).To(Equal(exitUsage))
	})

	It("refuses to run with the live gate closed (exit 4)", func() {
		code := cmdAgent(context.Background(), filepath.Join(tmp, "db.sqlite"), &flags{db: filepath.Join(tmp, "db.sqlite")}, "s1", os.Stdout, os.Stderr)
		Expect(code).To(Equal(exitConfigInvalid))
	})

	It("routes as a session subcommand", func() {
		fl, pos, err := parseFlags([]string{"session", "agent", "--db", filepath.Join(tmp, "db.sqlite"), "abc"})
		Expect(err).NotTo(HaveOccurred())
		Expect(pos).To(ContainElements("session", "agent", "abc"))
		_ = fl
	})
})
