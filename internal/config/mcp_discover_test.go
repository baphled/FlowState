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
		tmpDir   string
		origPAT  string
		origHome string
	)

	BeforeEach(func() {
		tmpDir = GinkgoT().TempDir()
		origPAT = os.Getenv("PATH")
		// Isolate HOME by default so individual contexts opt back in
		// when they want to drive the install-location-first probe.
		// Without this, any developer who has booted FlowState picks
		// up their real `~/.local/share/flowstate/memory-tools/`
		// payload and surprises the assertions below.
		origHome = os.Getenv("HOME")
		os.Setenv("HOME", GinkgoT().TempDir())
	})

	AfterEach(func() {
		os.Setenv("PATH", origPAT)
		os.Setenv("HOME", origHome)
	})

	Context("when mcp-mem0-server is in PATH", func() {
		BeforeEach(func() {
			fakeBin := filepath.Join(tmpDir, "mcp-mem0-server")
			Expect(os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o600)).To(Succeed())
			Expect(os.Chmod(fakeBin, 0o755)).To(Succeed())
			os.Setenv("PATH", tmpDir+":"+origPAT)
		})

		It("includes memory config", func() {
			servers := config.DiscoverMCPServers()

			var found bool
			for _, s := range servers {
				if s.Name == "memory" {
					found = true
					Expect(s.Command).To(ContainSubstring("mcp-mem0-server"))
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

		It("does not include flowstate-memory config", func() {
			servers := config.DiscoverMCPServers()

			for _, s := range servers {
				Expect(s.Name).NotTo(Equal("flowstate-memory"))
			}
		})
	})

	// Auto-materialisation drops the mem0 wrapper into
	// `~/.local/share/flowstate/memory-tools/` on first run, *not* onto
	// PATH. Discovery has to probe that install location explicitly so
	// fresh users get memory wiring without any extra step. Mirrors the
	// install path used by `flowstate memory-tools install` so the two
	// flows agree on where the binary lives.
	Context("when mcp-mem0-server is at the install location but not in PATH", func() {
		var origHome string

		BeforeEach(func() {
			origHome = os.Getenv("HOME")
			fakeHome := GinkgoT().TempDir()
			os.Setenv("HOME", fakeHome)

			installDir := filepath.Join(fakeHome, ".local", "share", "flowstate", "memory-tools")
			Expect(os.MkdirAll(installDir, 0o755)).To(Succeed())
			fakeBin := filepath.Join(installDir, "mcp-mem0-server")
			Expect(os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o600)).To(Succeed())
			Expect(os.Chmod(fakeBin, 0o755)).To(Succeed())

			os.Setenv("PATH", tmpDir) // PATH deliberately empty of mcp-mem0-server.
		})

		AfterEach(func() {
			os.Setenv("HOME", origHome)
		})

		It("discovers the install-location binary", func() {
			servers := config.DiscoverMCPServers()

			var found bool
			for _, s := range servers {
				if s.Name == "memory" {
					found = true
					Expect(s.Command).To(ContainSubstring(filepath.Join(".local", "share", "flowstate", "memory-tools", "mcp-mem0-server")))
					Expect(s.Enabled).To(BeTrue())
				}
			}
			Expect(found).To(BeTrue(),
				"discovery must probe the install location first so freshly-bootstrapped fresh users get memory without PATH gymnastics")
		})
	})

	// Operators with a pre-existing PATH-resident mem0 binary must keep
	// working — the install-location-first probe falls back to PATH when
	// the install dir is empty.
	Context("when mcp-mem0-server is on PATH but the install location is empty", func() {
		var origHome string

		BeforeEach(func() {
			origHome = os.Getenv("HOME")
			fakeHome := GinkgoT().TempDir()
			os.Setenv("HOME", fakeHome)
			// install location intentionally NOT created.

			fakeBin := filepath.Join(tmpDir, "mcp-mem0-server")
			Expect(os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o600)).To(Succeed())
			Expect(os.Chmod(fakeBin, 0o755)).To(Succeed())
			os.Setenv("PATH", tmpDir+":"+origPAT)
		})

		AfterEach(func() {
			os.Setenv("HOME", origHome)
		})

		It("falls back to PATH lookup", func() {
			servers := config.DiscoverMCPServers()

			var found bool
			for _, s := range servers {
				if s.Name == "memory" {
					found = true
					Expect(s.Command).To(Equal(filepath.Join(tmpDir, "mcp-mem0-server")))
				}
			}
			Expect(found).To(BeTrue(),
				"operators with a PATH-resident mcp-mem0-server must keep working when the install location is empty")
		})
	})
})
