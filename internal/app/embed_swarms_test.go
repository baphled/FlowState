package app_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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

		It("pins the example-tree A-Team prompts to the canonical coord-store key contract", func() {
			// Three-way contract pin (Slice B, May 2026 — bug-fix
			// `A-Team Swarm Key Contract Alignment`). The embedded
			// swarm yml above declares `output_key: output` for the
			// post-member relevance gate target (researcher), and the
			// bundled relevance-gate manifest reads `${target}/output`.
			// The legacy example-tree prompts under
			// examples/swarms/a-team/agents/ historically wrote to and
			// read from `a-team/{chainID}/research` — three files,
			// three different conventions. This spec pins the chosen
			// canonical key contract (vault note: A-Team Swarm
			// Integration (May 2026)):
			//
			//   coordinator → a-team/{chainID}/task-plan
			//   researcher  → a-team/{chainID}/output    (NOT /research)
			//   strategist  → a-team/{chainID}/strategy
			//   critic      → a-team/{chainID}/critique
			//   writer      → a-team/{chainID}/final-output
			//
			// The example tree is the operator-facing reference copy
			// folks `cp -r` from when standing up a custom A-Team. If
			// it drifts off the canonical contract again, the
			// relevance gate's payload composition (which reads
			// `${target}/output`) silently misses the researcher's
			// findings — the failure mode this spec exists to prevent.
			exampleAgents := filepath.Join("..", "..", "examples", "swarms", "a-team", "agents")

			researcherPrompt, err := os.ReadFile(filepath.Join(exampleAgents, "researcher.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(researcherPrompt)).To(ContainSubstring("a-team/{chainID}/output"),
				"researcher prompt must write to a-team/{chainID}/output to align with the relevance-gate's ${target}/output input")
			Expect(string(researcherPrompt)).NotTo(ContainSubstring("a-team/{chainID}/research"),
				"researcher prompt still references the retired /research key — see vault note 'A-Team Swarm Key Contract Alignment'")

			for _, member := range []string{"strategist.md", "critic.md", "writer.md"} {
				body, err := os.ReadFile(filepath.Join(exampleAgents, member))
				Expect(err).NotTo(HaveOccurred(), "reading %s", member)
				Expect(string(body)).NotTo(ContainSubstring("a-team/{chainID}/research"),
					"%s still reads from the retired /research key — must read /output to match the canonical contract", member)
			}
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

		// Meta-Swarm Coordinator Architecture (May 2026).
		//
		// The coordinator was historically the lead of a-team, with a
		// hard-coded A-Team persona that opened with "You are the lead
		// of the A-Team swarm" and instructed the model to write a
		// routing plan to `a-team/{chainID}/task-plan` before doing
		// anything else. That persona made @coordinator a-team-specific
		// and caused FlowState session dbcbe384 (52 messages, zero
		// delegate calls) to inspect stale coord_store entries instead
		// of dispatching members.
		//
		// New design: coordinator is a polymorphic swarm orchestrator
		// driven entirely by the engine's Swarm Leadership block (see
		// engine.go::appendSwarmLeadSectionFor). Coordinator leads a
		// new top-level swarm `meta-swarm` whose members ARE OTHER
		// SWARMS (a-team, dev-swarm, planning-loop, board-room) — true
		// three-tier orchestration: user → coordinator → sub-swarm →
		// members.
		It("contains the bundled meta-swarm.yml", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())

			body, err := fs.ReadFile(swarmsDir, "meta-swarm.yml")
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring("id: meta-swarm"))
		})

		It("parses meta-swarm.yml with coordinator as lead and sub-swarms as members", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "meta-swarm.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			Expect(m.SchemaVersion).To(Equal(swarm.SchemaVersionV1))
			Expect(m.ID).To(Equal("meta-swarm"))
			Expect(m.Lead).To(Equal("coordinator"))
			// Members are SWARM ids; they must be the four canonical
			// sub-swarms the coordinator routes between.
			Expect(m.Members).To(ConsistOf(
				"a-team",
				"dev-swarm",
				"planning-loop",
				"board-room",
			))
			Expect(m.AutoDispatchOnLead).To(BeTrue(),
				"@coordinator must auto-dispatch meta-swarm so the lead-block renders "+
					"and the model sees its sub-swarm members instead of running solo")
			Expect(m.Context.ChainPrefix).To(Equal("meta"))
			Expect(m.Validate(nil)).To(Succeed())
		})

		It("bundles a generic coordinator persona with no a-team-specific routing language", func() {
			// Phase 1 of the meta-swarm rewiring: the bundled
			// coordinator.md must NOT mention a-team or its
			// coord_store routing keys (`a-team/{chainID}/task-plan`,
			// `a-team/{chainID}/final-output`, etc.). The persona now
			// describes the lead behaviour generically so the same
			// agent file can lead meta-swarm, a-team (legacy), or any
			// future swarm without persona surgery.
			agentsDir, err := fs.Sub(app.EmbeddedAgentsFS(), "agents")
			Expect(err).NotTo(HaveOccurred())

			body, err := fs.ReadFile(agentsDir, "coordinator.md")
			Expect(err).NotTo(HaveOccurred())

			persona := string(body)
			Expect(persona).NotTo(ContainSubstring("A-Team"),
				"coordinator persona must be generic — A-Team reference belongs in the swarm "+
					"manifest, not the agent prompt")
			Expect(persona).NotTo(ContainSubstring("a-team"),
				"coordinator persona must be generic — a-team reference belongs in the swarm "+
					"manifest, not the agent prompt")
		})

		// Structural audit — gate vs agent-manifest output contract.
		//
		// Pins the load-bearing invariant that every post-member gate
		// referencing a JSON schema (or the bug-findings-v1
		// evidence-grounding shape) has its target agent's manifest
		// EXPLICITLY promise to emit that schema. Drift here is the bug
		// class that shipped in commit dff52884 — dev-swarm/engineer-swarm
		// each landed with `builtin:result-schema` gates pointing at
		// schema_refs the named agents never mention in their manifests,
		// halting the swarm on every run with `schema validation failed:
		// required: missing properties: ["verdict"]` (see vault note
		// "Code-Reviewer Not Found After Recent Changes (May 2026)").
		//
		// Rule: for every post-member gate the audit knows how to read,
		// the target agent's manifest must contain the gate's schema_ref
		// (for builtin:result-schema) or `bug-findings-v1` (for
		// builtin:evidence-grounding). ext:* gates are out of scope —
		// they are validated by their own manifests under
		// internal/app/gates/. This audit is intentionally narrow: it
		// catches the EXACT drift class introduced by dff52884, not a
		// general swarm validator.
		It("pins every post-member gate's schema contract to the target agent's manifest", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			agentsDir, err := fs.Sub(app.EmbeddedAgentsFS(), "agents")
			Expect(err).NotTo(HaveOccurred())

			swarmEntries, err := fs.ReadDir(swarmsDir, ".")
			Expect(err).NotTo(HaveOccurred())

			type gateAudit struct {
				swarm  string
				gate   swarm.GateSpec
				expect string // the schema name the target manifest must mention
			}
			var audits []gateAudit
			for _, entry := range swarmEntries {
				if entry.IsDir() {
					continue
				}
				name := entry.Name()
				if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
					continue
				}
				body, err := fs.ReadFile(swarmsDir, name)
				Expect(err).NotTo(HaveOccurred(), "reading %s", name)

				var m swarm.Manifest
				Expect(yaml.Unmarshal(body, &m)).To(Succeed(), "parsing %s", name)

				for _, gate := range m.Harness.Gates {
					if gate.When != swarm.LifecyclePostMember {
						continue
					}
					var expect string
					switch {
					case gate.Kind == "builtin:result-schema":
						expect = gate.SchemaRef
					case gate.Kind == swarm.EvidenceGroundingGateKind:
						// The evidence-grounding runner projects the
						// bug-findings-v1 shape (see internal/swarm/
						// gate_evidence_grounding.go:115); agents
						// gated by it must promise that shape.
						expect = "bug-findings-v1"
					default:
						// ext:* gates carry their input contract on
						// the gate manifest under internal/app/gates/,
						// not on the swarm YAML. Out of scope for this
						// audit.
						continue
					}
					audits = append(audits, gateAudit{
						swarm:  m.ID,
						gate:   gate,
						expect: expect,
					})
				}
			}

			// At least the planning-loop swarm always contributes
			// audits; if the loop ever yields zero, the swarm
			// directory got rewired and this audit is now silent.
			Expect(audits).NotTo(BeEmpty(),
				"audit yielded zero post-member result-schema/evidence-grounding gates — "+
					"the swarm directory may have been rewired; re-verify the discovery loop")

			swarmIDs := make(map[string]struct{})
			memberCount := 0
			for _, a := range audits {
				swarmIDs[a.swarm] = struct{}{}
				memberCount++

				// `gate.Target` is the agent id; the manifest file is
				// named `<id>.md` under EmbeddedAgentsFS / agents/.
				manifestName := a.gate.Target + ".md"
				manifest, err := fs.ReadFile(agentsDir, manifestName)
				Expect(err).NotTo(HaveOccurred(),
					"swarm %q gate %q targets agent %q but %q is not in the embedded agent set",
					a.swarm, a.gate.Name, a.gate.Target, manifestName)

				Expect(string(manifest)).To(ContainSubstring(a.expect),
					"swarm %q gate %q (kind=%q, schema_ref=%q) expects agent %q to emit %q, "+
						"but the bundled manifest %q never mentions that schema — this is the "+
						"dff52884 drift class (gate enforces a contract the agent manifest does "+
						"not promise). Fix: either change the gate's schema_ref / kind to match "+
						"what the agent actually emits, or update the agent manifest to promise "+
						"the schema_ref the gate enforces.",
					a.swarm, a.gate.Name, a.gate.Kind, a.gate.SchemaRef, a.gate.Target,
					a.expect, manifestName)
			}

			// Coverage breadcrumb: audit visited at least dev-swarm,
			// engineer-swarm, and planning-loop (the three in-repo
			// swarms with builtin:result-schema gates post-fix).
			Expect(swarmIDs).To(HaveKey("planning-loop"),
				"planning-loop ships builtin:result-schema gates and must be covered by this audit")
			Expect(swarmIDs).To(HaveKey("dev-swarm"),
				"dev-swarm ships gates introduced by dff52884 and must be covered by this audit "+
					"(the originating drift case)")
			Expect(swarmIDs).To(HaveKey("engineer-swarm"),
				"engineer-swarm ships gates introduced by dff52884 and must be covered by this audit")
		})

		// Drift-1 specific pin — dev-swarm Code-Reviewer gate.
		//
		// The audit above catches the general class; this spec pins
		// the SPECIFIC contract chosen for the fix so a future
		// re-introduction of the `code-review-verdict-v1`
		// builtin:result-schema gate (without updating the
		// Code-Reviewer manifest to emit the verdict shape) flips
		// red here with a targeted message naming the swarm + gate.
		It("dev-swarm Code-Reviewer gate is contract-aligned with the agent's bug-findings-v1 output", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "dev-swarm.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			var found bool
			for _, gate := range m.Harness.Gates {
				if gate.Target != "Code-Reviewer" || gate.When != swarm.LifecyclePostMember {
					continue
				}
				found = true
				Expect(gate.Kind).To(Equal(swarm.EvidenceGroundingGateKind),
					"dev-swarm Code-Reviewer gate must be builtin:evidence-grounding so the "+
						"agent's bug-findings-v1 output is validated against its cited file "+
						"snippets; a return to builtin:result-schema + code-review-verdict-v1 "+
						"is the dff52884 regression")
				Expect(gate.SchemaRef).To(BeEmpty(),
					"builtin:evidence-grounding gates take no schema_ref — the runner projects "+
						"bug-findings-v1 natively (see internal/swarm/gate_evidence_grounding.go)")
			}
			Expect(found).To(BeTrue(),
				"expected at least one post-member Code-Reviewer gate on dev-swarm")
		})

		// Drift-2 specific pin — engineer-swarm QA-Engineer gate.
		It("engineer-swarm QA-Engineer gate is contract-aligned with the agent's bug-findings-v1 output", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "engineer-swarm.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			var found bool
			for _, gate := range m.Harness.Gates {
				if gate.Target != "QA-Engineer" || gate.When != swarm.LifecyclePostMember {
					continue
				}
				found = true
				Expect(gate.Kind).To(Equal(swarm.EvidenceGroundingGateKind),
					"engineer-swarm QA-Engineer gate must be builtin:evidence-grounding to "+
						"match the agent's bug-findings-v1 output; review-verdict-v1 is the "+
						"dff52884 drift")
				Expect(gate.SchemaRef).To(BeEmpty(),
					"builtin:evidence-grounding gates take no schema_ref")
			}
			Expect(found).To(BeTrue(),
				"expected at least one post-member QA-Engineer gate on engineer-swarm")
		})

		// Drift-3 specific pin — dev-swarm Researcher gate.
		//
		// Chosen fix is option (a): drop the gate entirely. The
		// researcher's manifest emits prose markdown at
		// `a-team/{chainID}/output` (the path is hard-coded to the
		// a-team chain_prefix, not the dispatching swarm's prefix),
		// and dev-swarm's downstream consumers (Tech-Lead,
		// Senior-Engineer) read the prose conversationally — no
		// structured contract to enforce. Rewriting the gate to read
		// markdown (option b) or rewriting Researcher.md to emit JSON
		// (option c) both invite churn for no consumer benefit.
		It("dev-swarm has no Researcher post-member gate — drift-3 resolved by drop", func() {
			swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
			Expect(err).NotTo(HaveOccurred())
			body, err := fs.ReadFile(swarmsDir, "dev-swarm.yml")
			Expect(err).NotTo(HaveOccurred())

			var m swarm.Manifest
			Expect(yaml.Unmarshal(body, &m)).To(Succeed())

			for _, gate := range m.Harness.Gates {
				Expect(gate.Target).NotTo(Equal("Researcher"),
					"dev-swarm must not declare a post-member gate against Researcher — the "+
						"agent emits markdown at `a-team/{chainID}/output`, not JSON at "+
						"`dev-swarm/{chainID}/output`, so any structured gate against this "+
						"target is contract-drift (dff52884 introduced the failing "+
						"`post-member-researcher-evidence-bundle` gate; fix-option (a) drops it)")
			}
		})

		// Team-Lead pre-formatting anti-pattern (May 2026).
		//
		// Live session reproducer: a Team-Lead/coordinator agent
		// assembled a full multi-section markdown report (95-bug
		// summary) INSIDE its own assistant turns, then tried to
		// delegate only the "typing it out" step to a writer member.
		// The lead's existing anti-pattern block named code-reading
		// and code-writing as STOP violations but did NOT forbid
		// pre-formatting the deliverable itself in own prose. This
		// spec pins the new guidance that closes that gap.
		//
		// Loose substring matching deliberately: future prose tweaks
		// to the anti-pattern wording should be free, as long as the
		// guidance is still present and still names legal synthesis
		// targets the lead already has on its delegation_allowlist
		// (Researcher and/or Knowledge-Base-Curator). The targets
		// must be on the allowlist — adding `writer` or `analyst`
		// here would be a separate routing decision.
		It("Team-Lead.md forbids pre-formatting deliverables and routes synthesis to Researcher/KB-Curator", func() {
			agentsDir, err := fs.Sub(app.EmbeddedAgentsFS(), "agents")
			Expect(err).NotTo(HaveOccurred())

			body, err := fs.ReadFile(agentsDir, "Team-Lead.md")
			Expect(err).NotTo(HaveOccurred())
			persona := string(body)

			Expect(persona).To(ContainSubstring("Pre-formatting deliverables"),
				"Team-Lead.md must call out 'Pre-formatting deliverables' as an anti-pattern — "+
					"the live-session bug was a lead assembling a full markdown report inside "+
					"its own turn before handing it to a writer member to type")

			// The guidance must name at least one legal synthesis
			// target — both Researcher and Knowledge-Base-Curator are
			// on the lead's delegation_allowlist (lines 50-51). The
			// anti-pattern block (around lines 199-203) names other
			// agents elsewhere in the file, so we anchor on the
			// "Pre-formatting" sentence specifically.
			//
			// Find the line containing the anti-pattern and assert
			// it routes to a legal synthesis owner. Loose match — the
			// reviewer should be free to rewrite the surrounding
			// prose.
			lines := strings.Split(persona, "\n")
			var preFormatLine string
			for _, line := range lines {
				if strings.Contains(line, "Pre-formatting deliverables") {
					preFormatLine = line
					break
				}
			}
			Expect(preFormatLine).NotTo(BeEmpty(),
				"expected to locate the Pre-formatting deliverables anti-pattern bullet for "+
					"target-name assertion")
			Expect(preFormatLine).To(SatisfyAny(
				ContainSubstring("Researcher"),
				ContainSubstring("Knowledge-Base-Curator"),
			),
				"Pre-formatting deliverables anti-pattern must route synthesis to a legal "+
					"target on Team-Lead's delegation_allowlist (Researcher or "+
					"Knowledge-Base-Curator); adding `writer` or `analyst` here would be a "+
					"separate routing decision")
		})
	})
})
