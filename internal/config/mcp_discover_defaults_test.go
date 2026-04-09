package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("DiscoverMCPServers default Enabled values", Label("integration"), func() {
	var (
		tmpDir  string
		origPAT string
	)

	BeforeEach(func() {
		tmpDir = GinkgoT().TempDir()
		origPAT = os.Getenv("PATH")
	})

	AfterEach(func() {
		os.Setenv("PATH", origPAT)
	})

	Context("when mcp-mem0-server is available in PATH", func() {
		BeforeEach(func() {
			fakeBin := filepath.Join(tmpDir, "mcp-mem0-server")
			Expect(os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o600)).To(Succeed())
			Expect(os.Chmod(fakeBin, 0o755)).To(Succeed())
			os.Setenv("PATH", tmpDir+":"+origPAT)
		})

		It("returns the memory server with Enabled=true", func() {
			servers := config.DiscoverMCPServers()

			var found bool
			for _, s := range servers {
				if s.Name == "memory" {
					found = true
					Expect(s.Enabled).To(BeTrue())
				}
			}
			Expect(found).To(BeTrue())
		})
	})

	Context("when mcp-vault-server is available in PATH", func() {
		BeforeEach(func() {
			fakeBin := filepath.Join(tmpDir, "mcp-vault-server")
			Expect(os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o600)).To(Succeed())
			Expect(os.Chmod(fakeBin, 0o755)).To(Succeed())
			os.Setenv("PATH", tmpDir+":"+origPAT)
		})

		It("returns the vault-rag server with Enabled=true", func() {
			servers := config.DiscoverMCPServers()

			var found bool
			for _, s := range servers {
				if s.Name == "vault-rag" {
					found = true
					Expect(s.Enabled).To(BeTrue())
				}
			}
			Expect(found).To(BeTrue())
		})
	})

	Context("when no known servers are in PATH", func() {
		BeforeEach(func() {
			os.Setenv("PATH", tmpDir)
		})

		It("returns no servers", func() {
			servers := config.DiscoverMCPServers()
			Expect(servers).To(BeEmpty())
		})
	})

	Context("when all known servers are in PATH", func() {
		BeforeEach(func() {
			for _, name := range []string{"mcp-mem0-server", "mcp-vault-server", "npx"} {
				fakeBin := filepath.Join(tmpDir, name)
				Expect(os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o600)).To(Succeed())
				Expect(os.Chmod(fakeBin, 0o755)).To(Succeed())
			}
			os.Setenv("PATH", tmpDir)
		})

		It("returns every server with Enabled=true", func() {
			servers := config.DiscoverMCPServers()

			Expect(servers).NotTo(BeEmpty())
			for _, s := range servers {
				Expect(s.Enabled).To(BeTrue())
			}
		})

		It("includes all known server names", func() {
			servers := config.DiscoverMCPServers()

			names := make([]string, 0, len(servers))
			for _, s := range servers {
				names = append(names, s.Name)
			}
			Expect(names).To(ContainElements("memory", "vault-rag", "filesystem"))
		})
	})
})
