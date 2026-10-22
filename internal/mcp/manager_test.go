package mcp_test

import (
	"context"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
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
		Context("with InMemoryTransport", func() {
			var (
				server          *mcpsdk.Server
				serverErr       chan error
				clientTransport mcpsdk.Transport
				serverTransport mcpsdk.Transport
			)

			BeforeEach(func() {
				clientTransport, serverTransport = mcpsdk.NewInMemoryTransports()

				server = mcpsdk.NewServer(&mcpsdk.Implementation{
					Name:    "test-server",
					Version: "1.0.0",
				}, nil)

				serverErr = make(chan error, 1)
				go func() {
					serverErr <- server.Run(ctx, serverTransport)
				}()

				factory := func(_ context.Context, _ mcp.ServerConfig) (mcpsdk.Transport, error) {
					return clientTransport, nil
				}
				manager = mcp.NewManager(mcp.WithTransportFactory(factory))
			})

			AfterEach(func() {
				manager.DisconnectAll()
				Eventually(serverErr).Should(Receive())
			})

			It("adds server to the manager", func() {
				config := mcp.ServerConfig{
					Name:    "test-server",
					Command: "unused",
				}
				err := manager.Connect(ctx, config)
				Expect(err).NotTo(HaveOccurred())
				Expect(manager.ListServers()).To(ContainElement("test-server"))
			})
		})

		Context("when connecting with same name twice", func() {
			var (
				server          *mcpsdk.Server
				serverErr       chan error
				clientTransport mcpsdk.Transport
				serverTransport mcpsdk.Transport
				connectCount    int
			)

			BeforeEach(func() {
				connectCount = 0
				clientTransport, serverTransport = mcpsdk.NewInMemoryTransports()

				server = mcpsdk.NewServer(&mcpsdk.Implementation{
					Name:    "test-server",
					Version: "1.0.0",
				}, nil)

				serverErr = make(chan error, 1)
				go func() {
					serverErr <- server.Run(ctx, serverTransport)
				}()

				factory := func(_ context.Context, _ mcp.ServerConfig) (mcpsdk.Transport, error) {
					connectCount++
					if connectCount == 1 {
						return clientTransport, nil
					}
					ct, _ := mcpsdk.NewInMemoryTransports()
					return ct, nil
				}
				manager = mcp.NewManager(mcp.WithTransportFactory(factory))

				config := mcp.ServerConfig{
					Name:    "test-server",
					Command: "unused",
				}
				err := manager.Connect(ctx, config)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				manager.DisconnectAll()
				Eventually(serverErr).Should(Receive())
			})

			It("returns an error", func() {
				config := mcp.ServerConfig{
					Name:    "test-server",
					Command: "unused",
				}
				err := manager.Connect(ctx, config)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("already connected"))
			})
		})
	})

	Describe("Disconnect", func() {
		var (
			server          *mcpsdk.Server
			serverErr       chan error
			clientTransport mcpsdk.Transport
			serverTransport mcpsdk.Transport
		)

		BeforeEach(func() {
			clientTransport, serverTransport = mcpsdk.NewInMemoryTransports()

			server = mcpsdk.NewServer(&mcpsdk.Implementation{
				Name:    "test-server",
				Version: "1.0.0",
			}, nil)

			serverErr = make(chan error, 1)
			go func() {
				serverErr <- server.Run(ctx, serverTransport)
			}()

			factory := func(_ context.Context, _ mcp.ServerConfig) (mcpsdk.Transport, error) {
				return clientTransport, nil
			}
			manager = mcp.NewManager(mcp.WithTransportFactory(factory))

			config := mcp.ServerConfig{
				Name:    "test-server",
				Command: "unused",
			}
			err := manager.Connect(ctx, config)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			manager.DisconnectAll()
			Eventually(serverErr).Should(Receive())
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

	Describe("DisconnectAll", func() {
		var (
			server1        *mcpsdk.Server
			server2        *mcpsdk.Server
			serverErr1     chan error
			serverErr2     chan error
			transports     []mcpsdk.Transport
			transportIndex int
		)

		BeforeEach(func() {
			transportIndex = 0
			transports = make([]mcpsdk.Transport, 0, 2)

			ct1, st1 := mcpsdk.NewInMemoryTransports()
			ct2, st2 := mcpsdk.NewInMemoryTransports()
			transports = append(transports, ct1, ct2)

			server1 = mcpsdk.NewServer(&mcpsdk.Implementation{
				Name:    "server-1",
				Version: "1.0.0",
			}, nil)
			server2 = mcpsdk.NewServer(&mcpsdk.Implementation{
				Name:    "server-2",
				Version: "1.0.0",
			}, nil)

			serverErr1 = make(chan error, 1)
			serverErr2 = make(chan error, 1)
			go func() {
				serverErr1 <- server1.Run(ctx, st1)
			}()
			go func() {
				serverErr2 <- server2.Run(ctx, st2)
			}()

			factory := func(_ context.Context, _ mcp.ServerConfig) (mcpsdk.Transport, error) {
				t := transports[transportIndex]
				transportIndex++
				return t, nil
			}
			manager = mcp.NewManager(mcp.WithTransportFactory(factory))

			err := manager.Connect(ctx, mcp.ServerConfig{Name: "server-1", Command: "unused"})
			Expect(err).NotTo(HaveOccurred())
			err = manager.Connect(ctx, mcp.ServerConfig{Name: "server-2", Command: "unused"})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			Eventually(serverErr1).Should(Receive())
			Eventually(serverErr2).Should(Receive())
		})

		It("disconnects all servers", func() {
			Expect(manager.ListServers()).To(HaveLen(2))

			err := manager.DisconnectAll()
			Expect(err).NotTo(HaveOccurred())
			Expect(manager.ListServers()).To(BeEmpty())
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
			var (
				server          *mcpsdk.Server
				serverErr       chan error
				clientTransport mcpsdk.Transport
				serverTransport mcpsdk.Transport
			)

			BeforeEach(func() {
				clientTransport, serverTransport = mcpsdk.NewInMemoryTransports()

				server = mcpsdk.NewServer(&mcpsdk.Implementation{
					Name:    "test-server",
					Version: "1.0.0",
				}, nil)

				type EchoInput struct {
					Message string `json:"message"`
				}
				type EchoOutput struct {
					Result string `json:"result"`
				}

				mcpsdk.AddTool(server, &mcpsdk.Tool{
					Name:        "echo",
					Description: "Echo the input message",
				}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args EchoInput) (*mcpsdk.CallToolResult, EchoOutput, error) {
					return &mcpsdk.CallToolResult{
						Content: []mcpsdk.Content{
							&mcpsdk.TextContent{Text: "Echo: " + args.Message},
						},
					}, EchoOutput{Result: args.Message}, nil
				})

				serverErr = make(chan error, 1)
				go func() {
					serverErr <- server.Run(ctx, serverTransport)
				}()

				factory := func(_ context.Context, _ mcp.ServerConfig) (mcpsdk.Transport, error) {
					return clientTransport, nil
				}
				manager = mcp.NewManager(mcp.WithTransportFactory(factory))

				err := manager.Connect(ctx, mcp.ServerConfig{Name: "test-server", Command: "unused"})
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				manager.DisconnectAll()
				Eventually(serverErr).Should(Receive())
			})

			It("returns the tools from the server", func() {
				tools, err := manager.ListTools(ctx, "test-server")
				Expect(err).NotTo(HaveOccurred())
				Expect(tools).To(HaveLen(1))
				Expect(tools[0].Name).To(Equal("echo"))
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
			var (
				server          *mcpsdk.Server
				serverErr       chan error
				clientTransport mcpsdk.Transport
				serverTransport mcpsdk.Transport
			)

			BeforeEach(func() {
				clientTransport, serverTransport = mcpsdk.NewInMemoryTransports()

				server = mcpsdk.NewServer(&mcpsdk.Implementation{
					Name:    "test-server",
					Version: "1.0.0",
				}, nil)

				type EchoInput struct {
					Message string `json:"message"`
				}
				type EchoOutput struct {
					Result string `json:"result"`
				}

				mcpsdk.AddTool(server, &mcpsdk.Tool{
					Name:        "echo",
					Description: "Echo the input message",
				}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args EchoInput) (*mcpsdk.CallToolResult, EchoOutput, error) {
					return &mcpsdk.CallToolResult{
						Content: []mcpsdk.Content{
							&mcpsdk.TextContent{Text: "Echo: " + args.Message},
						},
					}, EchoOutput{Result: args.Message}, nil
				})

				serverErr = make(chan error, 1)
				go func() {
					serverErr <- server.Run(ctx, serverTransport)
				}()

				factory := func(_ context.Context, _ mcp.ServerConfig) (mcpsdk.Transport, error) {
					return clientTransport, nil
				}
				manager = mcp.NewManager(mcp.WithTransportFactory(factory))

				err := manager.Connect(ctx, mcp.ServerConfig{Name: "test-server", Command: "unused"})
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				manager.DisconnectAll()
				Eventually(serverErr).Should(Receive())
			})

			Context("when tool exists", func() {
				It("calls the tool and returns the result", func() {
					result, err := manager.CallTool(ctx, "test-server", "echo", map[string]any{
						"message": "hello",
					})
					Expect(err).NotTo(HaveOccurred())
					Expect(result).NotTo(BeNil())
					Expect(result.Content).To(ContainSubstring("Echo: hello"))
				})
			})

			Context("when tool does not exist", func() {
				It("returns an error", func() {
					_, err := manager.CallTool(ctx, "test-server", "nonexistent", nil)
					Expect(err).To(HaveOccurred())
				})
			})
		})

		Context("when tool call exceeds timeout", func() {
			var (
				slowServer      *mcpsdk.Server
				serverErr       chan error
				clientTransport mcpsdk.Transport
				serverTransport mcpsdk.Transport
			)

			BeforeEach(func() {
				clientTransport, serverTransport = mcpsdk.NewInMemoryTransports()

				slowServer = mcpsdk.NewServer(&mcpsdk.Implementation{
					Name:    "slow-server",
					Version: "1.0.0",
				}, nil)

				type SlowInput struct {
					Message string `json:"message"`
				}
				type SlowOutput struct {
					Result string `json:"result"`
				}

				mcpsdk.AddTool(slowServer, &mcpsdk.Tool{
					Name:        "slow-tool",
					Description: "A tool that takes too long",
				}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args SlowInput) (*mcpsdk.CallToolResult, SlowOutput, error) {
					select {
					case <-ctx.Done():
						return nil, SlowOutput{}, ctx.Err()
					case <-time.After(10 * time.Second):
						return &mcpsdk.CallToolResult{
							Content: []mcpsdk.Content{
								&mcpsdk.TextContent{Text: "done"},
							},
						}, SlowOutput{Result: "done"}, nil
					}
				})

				serverErr = make(chan error, 1)
				go func() {
					serverErr <- slowServer.Run(ctx, serverTransport)
				}()

				factory := func(_ context.Context, _ mcp.ServerConfig) (mcpsdk.Transport, error) {
					return clientTransport, nil
				}
				manager = mcp.NewManager(
					mcp.WithTransportFactory(factory),
					mcp.WithCallTimeout(100*time.Millisecond),
				)

				err := manager.Connect(ctx, mcp.ServerConfig{Name: "slow-server", Command: "unused"})
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				manager.DisconnectAll()
				Eventually(serverErr).Should(Receive())
			})

			It("returns a deadline exceeded error", func() {
				_, err := manager.CallTool(ctx, "slow-server", "slow-tool", map[string]any{
					"message": "hello",
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("deadline exceeded"))
			})
		})
	})

	Describe("ListServers", func() {
		var (
			transports     []mcpsdk.Transport
			servers        []*mcpsdk.Server
			serverErrs     []chan error
			transportIndex int
		)

		BeforeEach(func() {
			transportIndex = 0
			transports = make([]mcpsdk.Transport, 0, 3)
			servers = make([]*mcpsdk.Server, 0, 3)
			serverErrs = make([]chan error, 0, 3)

			for range 3 {
				ct, st := mcpsdk.NewInMemoryTransports()
				transports = append(transports, ct)

				s := mcpsdk.NewServer(&mcpsdk.Implementation{
					Name:    "test-server",
					Version: "1.0.0",
				}, nil)
				servers = append(servers, s)

				errCh := make(chan error, 1)
				serverErrs = append(serverErrs, errCh)
				go func(server *mcpsdk.Server, transport mcpsdk.Transport, ch chan error) {
					ch <- server.Run(ctx, transport)
				}(s, st, errCh)
			}

			factory := func(_ context.Context, _ mcp.ServerConfig) (mcpsdk.Transport, error) {
				t := transports[transportIndex]
				transportIndex++
				return t, nil
			}
			manager = mcp.NewManager(mcp.WithTransportFactory(factory))
		})

		AfterEach(func() {
			manager.DisconnectAll()
			for _, errCh := range serverErrs {
				Eventually(errCh).Should(Receive())
			}
		})

		It("returns sorted names", func() {
			err := manager.Connect(ctx, mcp.ServerConfig{Name: "zebra", Command: "unused"})
			Expect(err).NotTo(HaveOccurred())
			err = manager.Connect(ctx, mcp.ServerConfig{Name: "alpha", Command: "unused"})
			Expect(err).NotTo(HaveOccurred())
			err = manager.Connect(ctx, mcp.ServerConfig{Name: "beta", Command: "unused"})
			Expect(err).NotTo(HaveOccurred())

			servers := manager.ListServers()
			Expect(servers).To(Equal([]string{"alpha", "beta", "zebra"}))
		})
	})
})
