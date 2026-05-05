package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/zai"
)

const codingPlanHostURL = "https://api.z.ai/api/coding/paas/v4"

// zaiConfigWithPlanAndHost returns an AppConfig pre-populated with the
// supplied Z.AI plan/host pair so the zaiPlanFromConfig specs stay terse.
func zaiConfigWithPlanAndHost(plan, host string) *config.AppConfig {
	cfg := &config.AppConfig{}
	cfg.Providers.ZAI.Plan = plan
	cfg.Providers.ZAI.Host = host
	return cfg
}

var _ = Describe("zaiPlanFromConfig", func() {
	Context("when Plan is explicitly 'coding'", func() {
		It("returns the coding tag", func() {
			cfg := zaiConfigWithPlanAndHost("coding", "")
			Expect(zaiPlanFromConfig(cfg)).To(Equal(zai.PlanCoding))
		})

		It("normalises mixed-case and whitespace", func() {
			cfg := zaiConfigWithPlanAndHost("  Coding  ", "")
			Expect(zaiPlanFromConfig(cfg)).To(Equal(zai.PlanCoding))
		})
	})

	Context("when Plan is empty and Host equals the coding URL", func() {
		It("infers coding from the legacy host encoding", func() {
			cfg := zaiConfigWithPlanAndHost("", codingPlanHostURL)
			Expect(zaiPlanFromConfig(cfg)).To(Equal(zai.PlanCoding))
		})
	})

	Context("when both Plan and Host are empty", func() {
		It("returns the empty string (general)", func() {
			cfg := zaiConfigWithPlanAndHost("", "")
			Expect(zaiPlanFromConfig(cfg)).To(BeEmpty())
		})
	})

	Context("when Plan is 'general' and Host points at the coding URL", func() {
		It("honours the explicit Plan field over the legacy host", func() {
			cfg := zaiConfigWithPlanAndHost("general", codingPlanHostURL)
			Expect(zaiPlanFromConfig(cfg)).To(BeEmpty())
		})
	})
})

// buildPersistedPlanFile is the parser-aware constructor that
// PersistApprovedPlan now uses to build the plan.File written to disk.
// Closes the cosmetic regression where the older code crammed the raw
// plan markdown into TLDR (producing a persisted file with nested
// frontmatter and an empty Tasks list).
//
// Specs lock the contract: when the markdown carries valid YAML
// frontmatter, the parsed fields land on the File directly; the
// chainID is the fallback id/title; Status is forced to "approved" so
// downstream catalogue queries can filter; and Tasks is always
// populated via plan.TasksFromPlanText regardless of frontmatter shape.
var _ = Describe("buildPersistedPlanFile", func() {
	It("promotes parsed frontmatter fields onto the persisted File", func() {
		md := "---\n" +
			"id: my-plan\n" +
			"title: Add /version endpoint\n" +
			"description: short summary\n" +
			"---\n\n" +
			"# Body\n\n## Tasks\n\n### Task 1: Define struct\n### Task 2: Wire mux\n"

		f := buildPersistedPlanFile("chain-1", md)

		Expect(f.ID).To(Equal("my-plan"),
			"the frontmatter id wins over the chainID-derived default")
		Expect(f.Title).To(Equal("Add /version endpoint"),
			"the frontmatter title wins over the chainID-derived default")
		Expect(f.Description).To(Equal("short summary"))
		Expect(f.Status).To(Equal("approved"),
			"PersistApprovedPlan always stamps approved — that's its semantic")
		Expect(f.TLDR).To(Equal(""),
			"with a successful parse, TLDR stays unset; the previous bug "+
				"dumped the raw markdown here producing nested frontmatter")
		Expect(f.Tasks).NotTo(BeEmpty(),
			"tasks must always be extracted so downstream consumers see structured tasks")
	})

	It("falls back to chainID-derived id/title when the frontmatter omits them", func() {
		// Frontmatter present but missing id and title.
		md := "---\nstatus: draft\n---\n\n# Body without id/title\n\n## Tasks\n\n### Task 1: a\n"

		f := buildPersistedPlanFile("auto-fallback-chain", md)

		Expect(f.ID).To(Equal("auto-fallback-chain"))
		Expect(f.Title).To(Equal("Plan auto-fallback-chain"))
		Expect(f.Status).To(Equal("approved"))
	})

	It("falls back to TLDR + chainID defaults when the markdown lacks frontmatter", func() {
		// No leading "---" block at all — ParseFile returns an error.
		raw := "# Just a heading\n\n## Tasks\n\n### Task 1: still parseable\n"

		f := buildPersistedPlanFile("no-frontmatter-chain", raw)

		Expect(f.ID).To(Equal("no-frontmatter-chain"))
		Expect(f.Title).To(Equal("Plan no-frontmatter-chain"))
		Expect(f.Status).To(Equal("approved"))
		Expect(f.TLDR).To(Equal(raw),
			"on the failure path TLDR keeps the raw payload so the operator "+
				"can still read the plan body even though it could not be parsed")
		// TasksFromPlanText anchors the body off the frontmatter
		// delimiters; on a payload that lacks them entirely the parser
		// returns []. The auto-persist path is best-effort here — the
		// plan still lands on disk with a readable TLDR but Tasks is
		// empty. plan-writer's primary flow always emits frontmatter,
		// so this branch is only exercised by tests / direct callers
		// who hand us malformed input.
		Expect(f.Tasks).To(BeEmpty(),
			"frontmatter-less input cannot be parsed for tasks; "+
				"the file still persists with TLDR carrying the raw body")
	})

	It("forces Status=approved even when the source frontmatter says draft", func() {
		md := "---\nid: x\ntitle: t\nstatus: draft\n---\n\n# body\n"

		f := buildPersistedPlanFile("chain", md)

		Expect(f.Status).To(Equal("approved"),
			"persisted plans are by-definition approved at this entry point")
	})

	It("preserves a non-zero CreatedAt from the frontmatter", func() {
		md := "---\nid: x\ntitle: t\ncreated_at: 2024-06-01T12:00:00Z\n---\n\n# body\n"

		f := buildPersistedPlanFile("chain", md)
		Expect(f.CreatedAt.IsZero()).To(BeFalse())
		Expect(f.CreatedAt.Year()).To(Equal(2024))
	})

	It("populates a fresh CreatedAt when the frontmatter omits it", func() {
		md := "---\nid: x\ntitle: t\n---\n\n# body\n"

		f := buildPersistedPlanFile("chain", md)
		Expect(f.CreatedAt.IsZero()).To(BeFalse(),
			"every persisted plan must carry a CreatedAt so plan_list can sort/format")
	})

	It("returns a typed plan.File so downstream Store.Create accepts it", func() {
		md := "---\nid: typecheck\ntitle: t\n---\n\n# body\n"
		// Compile-time type pin: assignment to a plan.File-typed variable
		// fails if buildPersistedPlanFile ever drifts to a different shape.
		acceptsFile := func(plan.File) {}
		acceptsFile(buildPersistedPlanFile("chain", md))
	})
})

var _ = Describe("agentToProviderPreferences", func() {
	It("converts an empty slice to an empty slice", func() {
		result := agentToProviderPreferences(nil)
		Expect(result).To(BeEmpty())
	})

	It("preserves provider and model fields from each manifest preference", func() {
		prefs := []agent.ModelPreference{
			{Provider: "anthropic", Model: "claude-sonnet-4"},
			{Provider: "openai", Model: "gpt-4o"},
		}

		result := agentToProviderPreferences(prefs)

		Expect(result).To(HaveLen(2))
		Expect(result[0]).To(Equal(provider.ModelPreference{Provider: "anthropic", Model: "claude-sonnet-4"}))
		Expect(result[1]).To(Equal(provider.ModelPreference{Provider: "openai", Model: "gpt-4o"}))
	})

	It("preserves declaration order so failover respects manifest priority", func() {
		prefs := []agent.ModelPreference{
			{Provider: "zai", Model: "glm-4.7"},
			{Provider: "anthropic", Model: "claude-haiku-4"},
		}

		result := agentToProviderPreferences(prefs)

		Expect(result[0].Provider).To(Equal("zai"),
			"first manifest preference must remain first — failover uses list order")
	})
})

// buildAppWithPlugins returns a minimal App wired with a pluginRuntime
// that carries both a HealthManager and a failoverManager pre-seeded
// with parentPrefs. Avoids the full New() constructor so tests stay
// fast and free of filesystem I/O.
func buildAppWithPlugins(parentPrefs []provider.ModelPreference) *App {
	healthMgr := failover.NewHealthManager()
	reg := provider.NewRegistry()

	parentMgr := failover.NewManager(reg, healthMgr, 0)
	parentMgr.SetBasePreferences(parentPrefs)

	return &App{
		Registry:         agent.NewRegistry(),
		providerRegistry: reg,
		plugins: &pluginRuntime{
			healthManager:   healthMgr,
			failoverManager: parentMgr,
		},
	}
}

var _ = Describe("createDelegateEngine child failover preference building", func() {
	var (
		parentPrefs = []provider.ModelPreference{
			{Provider: "anthropic", Model: "claude-sonnet-4"},
			{Provider: "openai", Model: "gpt-4o"},
		}
		manifestPrefs = []agent.ModelPreference{
			{Provider: "zai", Model: "glm-4.7"},
		}
		store = coordination.NewMemoryStore()
	)

	Context("permissive manifest with preferred models", func() {
		It("appends parent preferences as deduplicated fallback after manifest models", func() {
			app := buildAppWithPlugins(parentPrefs)

			manifest := agent.Manifest{
				ID:              "analyst",
				Name:            "Analyst",
				ModelPolicy:     agent.ModelPolicyPermissive,
				PreferredModels: manifestPrefs,
			}
			app.Registry.Register(&manifest)

			eng, _ := app.createDelegateEngine(manifest, store, nil)

			prefs := eng.FailoverManager().Preferences()
			Expect(prefs).To(HaveLen(3),
				"manifest model + 2 parent models = 3 preferences")
			Expect(prefs[0]).To(Equal(provider.ModelPreference{Provider: "zai", Model: "glm-4.7"}),
				"manifest model must come first")
			Expect(prefs[1]).To(Equal(provider.ModelPreference{Provider: "anthropic", Model: "claude-sonnet-4"}),
				"first parent preference appended second")
			Expect(prefs[2]).To(Equal(provider.ModelPreference{Provider: "openai", Model: "gpt-4o"}),
				"second parent preference appended last")
		})
	})

	Context("empty policy (defaults to permissive) manifest with preferred models", func() {
		It("appends parent preferences as fallback when ModelPolicy is empty", func() {
			app := buildAppWithPlugins(parentPrefs)

			manifest := agent.Manifest{
				ID:              "researcher",
				Name:            "Researcher",
				ModelPolicy:     "", // empty == permissive
				PreferredModels: manifestPrefs,
			}
			app.Registry.Register(&manifest)

			eng, _ := app.createDelegateEngine(manifest, store, nil)

			prefs := eng.FailoverManager().Preferences()
			Expect(len(prefs)).To(BeNumerically(">", len(manifestPrefs)),
				"parent preferences must be appended when policy is empty")
		})
	})

	Context("strict manifest with preferred models", func() {
		It("uses only the manifest's declared models — no parent fallback", func() {
			app := buildAppWithPlugins(parentPrefs)

			manifest := agent.Manifest{
				ID:              "security-agent",
				Name:            "Security Agent",
				ModelPolicy:     agent.ModelPolicyStrict,
				PreferredModels: manifestPrefs,
			}
			app.Registry.Register(&manifest)

			eng, _ := app.createDelegateEngine(manifest, store, nil)

			prefs := eng.FailoverManager().Preferences()
			Expect(prefs).To(HaveLen(1),
				"strict policy must not append parent preferences")
			Expect(prefs[0]).To(Equal(provider.ModelPreference{Provider: "zai", Model: "glm-4.7"}))
		})
	})

	Context("manifest with no preferred models", func() {
		It("inherits parent preferences unchanged", func() {
			app := buildAppWithPlugins(parentPrefs)

			manifest := agent.Manifest{
				ID:   "generic-agent",
				Name: "Generic Agent",
				// no PreferredModels
			}
			app.Registry.Register(&manifest)

			eng, _ := app.createDelegateEngine(manifest, store, nil)

			prefs := eng.FailoverManager().Preferences()
			Expect(prefs).To(Equal(parentPrefs),
				"when the manifest has no preferred models the child must mirror parent preferences")
		})
	})

	Context("permissive manifest where a manifest model is also in the parent list", func() {
		It("does not duplicate the preference that appears in both lists", func() {
			// Parent list contains anthropic/claude-sonnet-4.
			// Manifest also declares anthropic/claude-sonnet-4 — dedup must drop the duplicate.
			sharedPref := agent.ModelPreference{Provider: "anthropic", Model: "claude-sonnet-4"}
			app := buildAppWithPlugins(parentPrefs)

			manifest := agent.Manifest{
				ID:          "dedup-agent",
				Name:        "Dedup Agent",
				ModelPolicy: agent.ModelPolicyPermissive,
				PreferredModels: []agent.ModelPreference{
					sharedPref,
					{Provider: "zai", Model: "glm-4.7"},
				},
			}
			app.Registry.Register(&manifest)

			eng, _ := app.createDelegateEngine(manifest, store, nil)

			prefs := eng.FailoverManager().Preferences()

			// Count occurrences of anthropic/claude-sonnet-4.
			count := 0
			for _, p := range prefs {
				if p.Provider == "anthropic" && p.Model == "claude-sonnet-4" {
					count++
				}
			}
			Expect(count).To(Equal(1),
				"anthropic/claude-sonnet-4 appears in both lists but must only appear once")
		})
	})
})

// Pins the wiring contract that delegate engines installs the failover
// StreamHook on their hook chain. Before this fix, createDelegateEngine
// built the child hook chain WITHOUT passing failoverMgr — so a 429 /
// rate_limit error inside a delegation surfaced as a hard failure
// instead of triggering provider failover. The childFailoverMgr created
// further down in the same function was handed to engine.New as
// FailoverManager but went unused because the explicit HookChain
// shadowed engine.resolveHookChain's failover-only fallback path
// (see internal/engine/engine.go:480 — cfg.HookChain wins, so the
// FailoverManager-derived default chain is bypassed).
//
// The assertion uses chain length as the wiring tell: with no
// learningStore/dispatcher/twc the delegate chain is logger +
// skill-autoloader + phase-detector + context-injection + tracer = 5,
// and adding the failover StreamHook brings it to 6. Symmetrical with
// harness_adapter_test.go's BuildHookChainForTest assertion which pins
// the root chain at the same baseline.
var _ = Describe("createDelegateEngine failover hook wiring", func() {
	var (
		parentPrefs = []provider.ModelPreference{
			{Provider: "anthropic", Model: "claude-sonnet-4"},
		}
		store = coordination.NewMemoryStore()
	)

	Context("when the app carries a plugin runtime with a failover manager", func() {
		It("installs the failover StreamHook on the delegate's hook chain", func() {
			app := buildAppWithPlugins(parentPrefs)

			manifest := agent.Manifest{
				ID:   "delegate-with-failover",
				Name: "Delegate With Failover",
			}
			app.Registry.Register(&manifest)

			eng, _ := app.createDelegateEngine(manifest, store, nil)

			Expect(eng.FailoverManager()).NotTo(BeNil(),
				"sanity: childFailoverMgr should be wired onto the engine")

			chain := eng.HookChainForTesting()
			Expect(chain).NotTo(BeNil(),
				"createDelegateEngine must install a hook chain on every delegate engine")
			Expect(chain.Len()).To(Equal(6),
				"delegate chain must include failover StreamHook on top of the "+
					"5-hook baseline (logger + autoloader + phase + context + tracer); "+
					"a length of 5 means failoverMgr was not wired into hookChainConfig "+
					"and 429 errors during delegation will fail hard instead of failing over")
		})
	})
})
