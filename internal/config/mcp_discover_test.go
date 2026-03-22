package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("DiscoverMCPServers", func() {
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

	Context("when flowstate-memory-server is in PATH", func() {
		BeforeEach(func() {
			fakeBin := filepath.Join(tmpDir, "flowstate-memory-server")
			Expect(os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o600)).To(Succeed())
			Expect(os.Chmod(fakeBin, 0o755)).To(Succeed())
			os.Setenv("PATH", tmpDir+":"+origPAT)
		})

		It("includes flowstate-memory config", func() {
			servers := config.DiscoverMCPServers()

			var found bool
			for _, s := range servers {
				if s.Name == "flowstate-memory" {
					found = true
					Expect(s.Command).To(ContainSubstring("flowstate-memory-server"))
					Expect(s.Enabled).To(BeFalse())
				}
			}
			Expect(found).To(BeTrue())
		})
	})

	Context("when flowstate-memory-server is not in PATH", func() {
		BeforeEach(func() {
			os.Setenv("PATH", tmpDir)
		})

		It("does not include flowstate-memory config", func() {
			servers := config.DiscoverMCPServers()

			for _, s := range servers {
				Expect(s.Name).NotTo(Equal("flowstate-memory"))
			}
		})
	})
})
