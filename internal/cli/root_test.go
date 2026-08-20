package cli_test

import (
	"context"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
	mcpclient "github.com/baphled/flowstate/internal/mcp"
)

// recordingMCPClient is a hand-rolled mcpclient.Client that counts
// Connect calls per server name and DisconnectAll invocations. Each App
// construction draws a fresh instance from a shared factory, mirroring
// production where every App owns its own Manager and subprocess pairs.
type recordingMCPClient struct {
	connects        []string
	liveConnections map[string]int
	disconnectAlls  int
}

func newRecordingMCPClient() *recordingMCPClient {
	return &recordingMCPClient{liveConnections: make(map[string]int)}
}

func (r *recordingMCPClient) Connect(_ context.Context, cfg mcpclient.ServerConfig) error {
	r.connects = append(r.connects, cfg.Name)
	r.liveConnections[cfg.Name]++
	return nil
}

func (r *recordingMCPClient) Disconnect(name string) error {
	delete(r.liveConnections, name)
	return nil
}

func (r *recordingMCPClient) ListTools(_ context.Context, _ string) ([]mcpclient.ToolInfo, error) {
	return nil, nil
}

func (r *recordingMCPClient) CallTool(_ context.Context, _, _ string, _ map[string]any) (*mcpclient.ToolResult, error) {
	return &mcpclient.ToolResult{}, nil
}

func (r *recordingMCPClient) DisconnectAll() error {
	r.disconnectAlls++
	r.liveConnections = make(map[string]int)
	return nil
}

// isolateFlowstateUserDirs points HOME and the XDG base dirs at fresh
// temp directories so app.Bootstrap's seeding, the mem0 wrapper
// materialisation, and the permissions.yaml write never touch the
// operator's real locations. Restored via DeferCleanup.
func isolateFlowstateUserDirs() string {
	tmp := GinkgoT().TempDir()
	overrides := map[string]string{
		"HOME":             filepath.Join(tmp, "home"),
		"XDG_CONFIG_HOME":  filepath.Join(tmp, "config"),
		"XDG_DATA_HOME":    filepath.Join(tmp, "data"),
		"QDRANT_URL":       "",
		"FLOWSTATE_CONFIG": "",
	}
	originals := make(map[string]string, len(overrides))
	for name, value := range overrides {
		originals[name] = os.Getenv(name)
		Expect(os.Setenv(name, value)).To(Succeed())
	}
	DeferCleanup(func() {
		for name, value := range originals {
			if value == "" {
				Expect(os.Unsetenv(name)).To(Succeed())
			} else {
				Expect(os.Setenv(name, value)).To(Succeed())
			}
		}
	})
	return tmp
}

var _ = Describe("cli root app rebuild MCP lifecycle", func() {
	It("keeps exactly one live MCP connection per server across the bootstrap rebuild", func() {
		isolateFlowstateUserDirs()

		cfg := config.DefaultConfig()
		cfg.MCPServers = []config.MCPServerConfig{
			{Name: "alpha", Command: "unused-with-recording-client"},
		}

		var recorders []*recordingMCPClient
		application, err := app.NewWithOptions(cfg, app.NewOptions{
			SkipBootstrap: true,
			MCPClientFactory: func() mcpclient.Client {
				recorder := newRecordingMCPClient()
				recorders = append(recorders, recorder)
				return recorder
			},
		})
		Expect(err).NotTo(HaveOccurred())
		preRebuild := application
		Expect(preRebuild.BootstrapDeferred()).To(BeTrue(),
			"the serve startup path defers bootstrap so PersistentPreRunE rebuilds the App")

		root := cli.NewRootCmdFromAppPointer(&application)
		root.SetArgs([]string{"tools", "list"})
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		Expect(root.Execute()).To(Succeed())

		Expect(application).NotTo(BeIdenticalTo(preRebuild),
			"a bootstrap-annotated command must swap in the rebuilt App")
		Expect(recorders).To(HaveLen(2),
			"each App construction draws exactly one MCP client")
		Expect(recorders[0].liveConnections).To(BeEmpty(),
			"the pre-rebuild App's MCP manager must be torn down at the swap")
		Expect(recorders[0].disconnectAlls).To(Equal(1),
			"the pre-rebuild App's subprocess pairs are disconnected when the rebuild replaces it")
		Expect(recorders[1].liveConnections["alpha"]).To(Equal(1),
			"the rebuilt App holds exactly one live connection per configured server")
	})
})
