package app

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
)

// Plans/Child Session Turn Registry Plumbing (May 2026) §S8.1 —
// App-side wiring: the App-constructed DelegateTool and the API
// server's auto-constructed Dispatcher MUST share the same
// *turn.Registry instance pointer. Without shared-instance plumbing,
// PR2a's executeSync + PR2b's swarm-fan-out write per-child Turn
// lifecycles into a registry the API server's long-poll endpoint
// (handleGetTurn) does NOT read from, and the live-UI channel for
// delegate-spawned children stays dark even though the plumbing is
// in place at every other layer.
//
// PR2a engineer's deferred item: the dispatcher is constructed inside
// internal/api/server.go:477. Sharing the instance requires either
// moving Turn-registry ownership up to App OR adding a setter to the
// API server. PR2b lands Option A (minimum surgical): add a
// TurnRegistry() accessor on *api.Server that returns
// s.dispatcher.TurnRegistry() and wire the DelegateTool from it
// inside configureDelegateTool. Verified end-to-end here.

// fakeAPIStreamer satisfies api.Streamer for the auto-construct path
// at server.go:472-484. Production wires the engine's per-agent
// streaming.Streamer here; this fake is sufficient because api.NewServer
// only checks whether streamer != nil before invoking dispatch.New.
type fakeAPIStreamer struct{}

func (fakeAPIStreamer) Stream(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	return nil, nil
}

var _ = Describe("App-side Turn registry wiring (S8.1)", func() {
	It("makes the App-constructed DelegateTool and the api.Server's Dispatcher share the SAME *turn.Registry instance", func() {
		// Build a delegating coordinator manifest so
		// wireDelegateToolIfEnabled fires the AddTool branch
		// (not the RemoveTool branch).
		coordManifest := agent.Manifest{
			ID:   "coordinator-s81",
			Name: "Coordinator (S8.1)",
			Delegation: agent.Delegation{
				CanDelegate: true,
			},
		}
		targetManifest := agent.Manifest{
			ID:   "explorer-s81",
			Name: "Explorer (S8.1)",
			Capabilities: agent.Capabilities{
				Tools: []string{"read"},
			},
		}

		mockProv := &mockProvider{name: "anthropic"}
		providerReg := provider.NewRegistry()
		providerReg.Register(mockProv)
		application := &App{
			Registry:         agent.NewRegistry(),
			providerRegistry: providerReg,
			defaultProvider:  mockProv,
			Config: &config.AppConfig{
				ToolCapableModels:   []string{"*"},
				ToolIncapableModels: []string{},
			},
		}
		application.Registry.Register(&coordManifest)
		application.Registry.Register(&targetManifest)

		// Construct an api.Server with a non-nil Streamer so the
		// auto-construct branch at server.go:472 fires — this is
		// the load-bearing pre-condition for shared-instance
		// plumbing. Production wiring (app.go:784) always supplies
		// a real streamer; tests that skip it get a nil dispatcher
		// and the TurnRegistry() accessor returns nil (back-compat).
		registry := application.Registry
		manifests := registry.List()
		manifestValues := make([]agent.Manifest, len(manifests))
		for i, m := range manifests {
			manifestValues[i] = *m
		}
		disc := discovery.NewAgentDiscovery(manifestValues)
		apiServer := api.NewServer(
			fakeAPIStreamer{},
			registry,
			disc,
			[]skill.Skill{},
		)
		application.API = apiServer

		// The api.Server's auto-construct path must have wired a
		// non-nil *turn.Registry — pre-condition for the spec.
		serverRegistry := apiServer.TurnRegistry()
		Expect(serverRegistry).NotTo(BeNil(),
			"api.Server.TurnRegistry() must be non-nil when a streamer is wired (auto-construct path at server.go:472-484 + the TurnRegistry accessor below it) — without this the App-side wiring has no shared instance to install")

		// Build the coordinator's engine.
		coordEngine := engine.New(engine.Config{
			Manifest:      coordManifest,
			AgentRegistry: application.Registry,
			Registry:      providerReg,
			Tools:         []tool.Tool{&mockTool{name: "test-tool"}},
		})

		// Drive the production wiring path — exactly the call
		// configureApplicationAfterBuild makes at app.go:424 for
		// every delegating manifest. configureDelegateTool's new
		// branch threads api.Server.TurnRegistry() into the
		// DelegateTool's turnRegistry field.
		application.wireDelegateToolIfEnabled(coordEngine, coordManifest)

		dt, found := coordEngine.GetDelegateTool()
		Expect(found).To(BeTrue(),
			"wireDelegateToolIfEnabled must install a DelegateTool when can_delegate=true")

		toolRegistry := dt.TurnRegistry()
		Expect(toolRegistry).NotTo(BeNil(),
			"the wired DelegateTool's turn registry must be non-nil — without this, every per-child Turn lifecycle site short-circuits and the live-UI channel stays dark")

		// Load-bearing assertion: SAME instance pointer. Both
		// sides return the same *turn.Registry from
		// dispatcher.TurnRegistry() so BeIdenticalTo (== on the
		// pointer) holds. Any future refactor that constructs a
		// fresh registry on the App side would fail this assertion
		// loudly.
		Expect(toolRegistry).To(BeIdenticalTo(serverRegistry),
			"the App-constructed DelegateTool and the api.Server's Dispatcher MUST share the same *turn.Registry instance pointer — without this, per-child Turn lifecycles land on a registry the long-poll endpoint does not read from (S8.1 + Plans/Child Session Turn Registry Plumbing §Item 2d, PR2a engineer's deferred item)")
	})

	It("survives the nil-API path with the historical no-Turn-channel behaviour (back-compat)", func() {
		// When App.API is nil (pre-API test composition, or
		// minimal-Server fixture), configureDelegateTool must
		// leave the DelegateTool's turn registry as nil — every
		// downstream lifecycle site short-circuits, matching the
		// pre-PR2a no-Turn-channel behaviour for the dozens of
		// pre-plumbing NewDelegateTool callsites per D7.
		coordManifest := agent.Manifest{
			ID:   "coordinator-s81-noapi",
			Name: "Coordinator (S8.1 noapi)",
			Delegation: agent.Delegation{
				CanDelegate: true,
			},
		}

		mockProv := &mockProvider{name: "anthropic"}
		providerReg := provider.NewRegistry()
		providerReg.Register(mockProv)
		application := &App{
			Registry:         agent.NewRegistry(),
			providerRegistry: providerReg,
			defaultProvider:  mockProv,
			Config: &config.AppConfig{
				ToolCapableModels:   []string{"*"},
				ToolIncapableModels: []string{},
			},
			// App.API intentionally left nil.
		}
		application.Registry.Register(&coordManifest)

		coordEngine := engine.New(engine.Config{
			Manifest:      coordManifest,
			AgentRegistry: application.Registry,
			Registry:      providerReg,
			Tools:         []tool.Tool{&mockTool{name: "test-tool"}},
		})

		application.wireDelegateToolIfEnabled(coordEngine, coordManifest)

		dt, found := coordEngine.GetDelegateTool()
		Expect(found).To(BeTrue())

		// nil registry — back-compat path.
		Expect(dt.TurnRegistry()).To(BeNil(),
			"a nil App.API must leave the DelegateTool's turn registry nil so every lifecycle site short-circuits per D7 (back-compat regression pin)")
	})
})
