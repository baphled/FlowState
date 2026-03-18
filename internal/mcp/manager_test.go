package mcp_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/mcp"
)

var _ = Describe("Manager", func() {
	var (
		manager *mcp.Manager
		ctx     context.Context
	)

	BeforeEach(func() {
		manager = mcp.NewManager()
		ctx = context.Background()
	})

	Describe("NewManager", func() {
		It("returns an empty manager", func() {
			Expect(manager).NotTo(BeNil())
			Expect(manager.ListServers()).To(BeEmpty())
		})
	})

	Describe("Connect", func() {
		It("adds server to the manager", func() {
			config := mcp.ServerConfig{
				Name:    "test-server",
				Command: "echo",
				Args:    []string{"hello"},
			}
			err := manager.Connect(ctx, config)
			Expect(err).NotTo(HaveOccurred())
			Expect(manager.ListServers()).To(ContainElement("test-server"))
		})

		Context("when connecting with same name twice", func() {
			BeforeEach(func() {
				config := mcp.ServerConfig{
					Name:    "test-server",
					Command: "echo",
					Args:    []string{"hello"},
				}
				err := manager.Connect(ctx, config)
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error", func() {
				config := mcp.ServerConfig{
					Name:    "test-server",
					Command: "echo",
					Args:    []string{"world"},
				}
				err := manager.Connect(ctx, config)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("already connected"))
			})
		})
	})

	Describe("Disconnect", func() {
		BeforeEach(func() {
			config := mcp.ServerConfig{
				Name:    "test-server",
				Command: "echo",
				Args:    []string{"hello"},
			}
			err := manager.Connect(ctx, config)
			Expect(err).NotTo(HaveOccurred())
		})

		It("removes server from the manager", func() {
			err := manager.Disconnect("test-server")
			Expect(err).NotTo(HaveOccurred())
			Expect(manager.ListServers()).NotTo(ContainElement("test-server"))
		})

		Context("when disconnecting unknown server", func() {
			It("returns an error", func() {
				err := manager.Disconnect("unknown-server")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not found"))
			})
		})
	})

	Describe("ListTools", func() {
		Context("when server is not connected", func() {
			It("returns an error", func() {
				_, err := manager.ListTools(ctx, "unknown-server")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not found"))
			})
		})

		Context("when server is connected", func() {
			BeforeEach(func() {
				config := mcp.ServerConfig{
					Name:    "test-server",
					Command: "echo",
					Args:    []string{"hello"},
				}
				err := manager.Connect(ctx, config)
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns tools from the server", func() {
				tools, err := manager.ListTools(ctx, "test-server")
				Expect(err).NotTo(HaveOccurred())
				Expect(tools).NotTo(BeNil())
			})
		})
	})

	Describe("CallTool", func() {
		Context("when server is not connected", func() {
			It("returns an error", func() {
				_, err := manager.CallTool(ctx, "unknown-server", "tool", nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not found"))
			})
		})

		Context("when server is connected", func() {
			BeforeEach(func() {
				config := mcp.ServerConfig{
					Name:    "test-server",
					Command: "echo",
					Args:    []string{"hello"},
				}
				err := manager.Connect(ctx, config)
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns a result", func() {
				result, err := manager.CallTool(ctx, "test-server", "tool", map[string]any{"key": "value"})
				Expect(err).NotTo(HaveOccurred())
				Expect(result).NotTo(BeNil())
			})
		})
	})

	Describe("ListServers", func() {
		It("returns sorted names", func() {
			err := manager.Connect(ctx, mcp.ServerConfig{Name: "zebra", Command: "echo"})
			Expect(err).NotTo(HaveOccurred())
			err = manager.Connect(ctx, mcp.ServerConfig{Name: "alpha", Command: "echo"})
			Expect(err).NotTo(HaveOccurred())
			err = manager.Connect(ctx, mcp.ServerConfig{Name: "beta", Command: "echo"})
			Expect(err).NotTo(HaveOccurred())

			servers := manager.ListServers()
			Expect(servers).To(Equal([]string{"alpha", "beta", "zebra"}))
		})
	})
})
