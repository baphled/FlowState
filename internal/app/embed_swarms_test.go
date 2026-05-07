package app_test

import (
	"io/fs"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("EmbeddedSwarmsFS", func() {
	Context("when calling EmbeddedSwarmsFS", func() {
		It("returns a non-nil fs.FS", func() {
			Expect(app.EmbeddedSwarmsFS()).NotTo(BeNil())
		})

		It("contains the bundled planning-loop.yml", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())

			body, err := fs.ReadFile(swarmsDir, "planning-loop.yml")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("id: planning-loop"))
		})

		It("contains the bundled solo.yml", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())

			body, err := fs.ReadFile(swarmsDir, "solo.yml")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("id: solo"))
		})

		It("parses planning-loop.yml as a structurally valid swarm manifest", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "planning-loop.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			Expect(m.SchemaVersion).To(Equal(swarm.SchemaVersionV1))
			Expect(m.ID).To(Equal("planning-loop"))
			Expect(m.Lead).To(Equal("planner"))
			Expect(m.Members).To(ContainElements("explorer", "librarian", "analyst", "plan-writer", "plan-reviewer"))
			Expect(m.Validate(nil)).To(Succeed())
		})

		It("ships a post-member result-schema gate for every structured-output member", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "planning-loop.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			expected := map[string]string{
				"explorer":      swarm.EvidenceBundleV1Name,
				"librarian":     swarm.ExternalRefsV1Name,
				"analyst":       swarm.AnalysisBundleV1Name,
				"plan-writer":   swarm.PlanDocumentV1Name,
				"plan-reviewer": swarm.ReviewVerdictV1Name,
			}
			expectedKeys := map[string]string{
				"explorer":      "output",
				"librarian":     "output",
				"analyst":       "output",
				"plan-writer":   "output",
				"plan-reviewer": "review",
			}
			seen := make(map[string]string, len(expected))
			seenKeys := make(map[string]string, len(expected))
			for _, gate := range m.Harness.Gates {
				Expect(gate.Kind).To(Equal("builtin:result-schema"))
				Expect(gate.When).To(Equal(swarm.LifecyclePostMember))
				seen[gate.Target] = gate.SchemaRef
				seenKeys[gate.Target] = gate.OutputKey
			}
			Expect(seen).To(Equal(expected))
			Expect(seenKeys).To(Equal(expectedKeys))
		})

		It("parses solo.yml as a structurally valid swarm manifest", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "solo.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			Expect(m.ID).To(Equal("solo"))
			Expect(m.Lead).To(Equal("executor"))
			Expect(m.Validate(nil)).To(Succeed())
		})

		It("contains the bundled a-team.yml", func() {
			// A-Team is the generalist swarm that ships with the binary
			// alongside planning-loop and solo. It enforces topic-fit on
			// the researcher's output via the post-member relevance gate
			// shipped under internal/app/gates/relevance-gate/. Seeding
			// the manifest into cfg.SwarmDir is what makes `@a-team` a
			// resolvable swarm id at the registry level after `app.New`.
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())

			body, err := fs.ReadFile(swarmsDir, "a-team.yml")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("id: a-team"))
		})

		It("parses a-team.yml with five members and the post-member relevance gate", func() {
			// Pin the structural contract this slice ships: lead is
			// `coordinator`, members include the canonical generalist
			// roster (researcher, strategist, critic, writer, executor),
			// and the harness carries one ext:relevance-gate fired
			// post-member around the researcher with output_key=output.
			// The gate's multi-key inputs are declared on the gate
			// manifest (internal/app/gates/relevance-gate/manifest.yml),
			// not the swarm manifest, so the swarm-level assertion stays
			// member-shaped.
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "a-team.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			Expect(m.SchemaVersion).To(Equal(swarm.SchemaVersionV1))
			Expect(m.ID).To(Equal("a-team"))
			Expect(m.Lead).To(Equal("coordinator"))
			Expect(m.Members).To(ConsistOf("researcher", "strategist", "critic", "writer", "executor"))
			Expect(m.Context.ChainPrefix).To(Equal("a-team"))

			Expect(m.Harness.Gates).To(HaveLen(1))
			gate := m.Harness.Gates[0]
			Expect(gate.Name).To(Equal("post-member-researcher-relevance"))
			Expect(gate.Kind).To(Equal("ext:relevance-gate"))
			Expect(gate.When).To(Equal(swarm.LifecyclePostMember))
			Expect(gate.Target).To(Equal("researcher"))
			Expect(gate.OutputKey).To(Equal("output"))

			// File-load validation against the no-op validator runs the
			// scalar / gate-prefix / self-reference rules without
			// requiring a populated agent registry. The full registry-
			// aware re-validation happens in NewRegistryFromDir at
			// app.New time and is covered transitively by the existing
			// SwarmRegistry suite once the canonical agents (coordinator,
			// strategist, critic) ship in this same slice.
			Expect(m.Validate(nil)).To(Succeed())
		})

		It("registers @a-team in the swarm registry against the bundled agent set", func() {
			// End-to-end pin: seed the embedded swarms FS into a tmp dir,
			// build the agent registry from the embedded agent manifests
			// (so coordinator/strategist/critic + Researcher/Writer +
			// executor all live in one place), then confirm
			// NewRegistryFromDir resolves a-team's lead and members
			// without aggregated errors. This is the contract `@a-team`
			// at chat resolution time depends on — once this passes,
			// flipping the swarm-mention router on @a-team can't fail at
			// the registry level.
			swarmDest, err := os.MkdirTemp("", "embed-swarms-a-team-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(swarmDest) })
			Expect(app.SeedSwarmsDir(app.EmbeddedSwarmsFS(), swarmDest)).To(Succeed())

			agentDest, err := os.MkdirTemp("", "embed-agents-a-team-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(agentDest) })
			Expect(app.SeedAgentsDir(app.EmbeddedAgentsFS(), agentDest)).To(Succeed())

			agentReg := agent.NewRegistry()
			Expect(agentReg.Discover(filepath.Clean(agentDest))).To(Succeed())
			adapter := app.NewSwarmAgentRegistryAdapterForTest(agentReg)

			swarmReg, err := swarm.NewRegistryFromDir(swarmDest, adapter)
			Expect(err).NotTo(HaveOccurred(),
				"a-team must register with the bundled agent set; if this fails, "+
					"a member id likely fell out of sync with its agent manifest's id/aliases")

			loaded, ok := swarmReg.Get("a-team")
			Expect(ok).To(BeTrue(), "expected @a-team to resolve in the swarm registry")
			Expect(loaded.Lead).To(Equal("coordinator"))
		})

		It("contains the bundled board-room.yml", func() {
			// Board Room is the adversarial pitch-committee swarm that
			// ships alongside a-team and planning-loop. It enforces a
			// 3-round investment review protocol with the post-member
			// quorum-gate validating both the presence of all five
			// analyst positions and a genuine bull/bear divergence —
			// see internal/app/gates/quorum-gate/. Seeding the
			// manifest into cfg.SwarmDir is what makes `@board-room`
			// a resolvable swarm id at the registry level after
			// `app.New`.
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())

			body, err := fs.ReadFile(swarmsDir, "board-room.yml")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("id: board-room"))
		})

		It("parses board-room.yml with five analysts and the post-member quorum gate", func() {
			// Pin the structural contract this slice ships: lead is
			// `chair`, members are the five specialist analysts (bull,
			// bear, market, financial, technical), and the harness
			// carries one ext:quorum-gate fired post-member around the
			// last analyst (technical-analyst) with output_key=output.
			// The gate's multi-key inputs are declared on the gate
			// manifest (internal/app/gates/quorum-gate/manifest.yml),
			// not the swarm manifest, so the swarm-level assertion
			// stays member-shaped.
			//
			// Sequential dispatch (harness.parallel: false) is the
			// deliberate choice — HarnessConfig.Parallel is a single
			// boolean today with no per-member override, so sequential
			// roster-order dispatch is what guarantees all five
			// positions are present in the coord-store when the
			// post-technical-analyst gate fires. See the Board Room
			// vault feature note for the tradeoff write-up.
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "board-room.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			Expect(m.SchemaVersion).To(Equal(swarm.SchemaVersionV1))
			Expect(m.ID).To(Equal("board-room"))
			Expect(m.Lead).To(Equal("chair"))
			Expect(m.Members).To(ConsistOf(
				"bull-analyst",
				"bear-analyst",
				"market-analyst",
				"financial-analyst",
				"technical-analyst",
			))
			Expect(m.Context.ChainPrefix).To(Equal("board-room"))
			Expect(m.Harness.Parallel).To(BeFalse(),
				"sequential dispatch keeps all five positions in the coord-store when the post-member gate fires")

			Expect(m.Harness.Gates).To(HaveLen(1))
			gate := m.Harness.Gates[0]
			Expect(gate.Kind).To(Equal("ext:quorum-gate"))
			Expect(gate.When).To(Equal(swarm.LifecyclePostMember))
			Expect(gate.Target).To(Equal("technical-analyst"))
			Expect(gate.OutputKey).To(Equal("output"))

			// File-load validation against the no-op validator runs
			// the scalar / gate-prefix / self-reference rules without
			// requiring a populated agent registry. The full registry-
			// aware re-validation happens in NewRegistryFromDir at
			// app.New time and is covered by the next spec.
			Expect(m.Validate(nil)).To(Succeed())
		})

		It("registers @board-room in the swarm registry against the bundled agent set", func() {
			// End-to-end pin: seed the embedded swarms FS into a tmp
			// dir, build the agent registry from the embedded agent
			// manifests (so chair + the five analysts all live in one
			// place), then confirm NewRegistryFromDir resolves
			// board-room's lead and members without aggregated errors.
			// This is the contract `@board-room` at chat resolution
			// time depends on — once this passes, flipping the
			// swarm-mention router on @board-room can't fail at the
			// registry level.
			swarmDest, err := os.MkdirTemp("", "embed-swarms-board-room-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(swarmDest) })
			Expect(app.SeedSwarmsDir(app.EmbeddedSwarmsFS(), swarmDest)).To(Succeed())

			agentDest, err := os.MkdirTemp("", "embed-agents-board-room-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(agentDest) })
			Expect(app.SeedAgentsDir(app.EmbeddedAgentsFS(), agentDest)).To(Succeed())

			agentReg := agent.NewRegistry()
			Expect(agentReg.Discover(filepath.Clean(agentDest))).To(Succeed())
			adapter := app.NewSwarmAgentRegistryAdapterForTest(agentReg)

			swarmReg, err := swarm.NewRegistryFromDir(swarmDest, adapter)
			Expect(err).NotTo(HaveOccurred(),
				"board-room must register with the bundled agent set; if this fails, "+
					"a member id likely fell out of sync with its agent manifest's id/aliases")

			loaded, ok := swarmReg.Get("board-room")
			Expect(ok).To(BeTrue(), "expected @board-room to resolve in the swarm registry")
			Expect(loaded.Lead).To(Equal("chair"))
			Expect(loaded.Members).To(ConsistOf(
				"bull-analyst",
				"bear-analyst",
				"market-analyst",
				"financial-analyst",
				"technical-analyst",
			))
		})
	})
})
