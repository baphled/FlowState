package agent_test

import (
	"encoding/json"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

var _ = Describe("Manifest", func() {
	Describe("DefaultContextManagement", func() {
		It("returns sensible defaults", func() {
			defaults := agent.DefaultContextManagement()

			Expect(defaults.MaxRecursionDepth).To(Equal(2))
			Expect(defaults.SummaryTier).To(Equal("quick"))
			Expect(defaults.SlidingWindowSize).To(Equal(10))
			Expect(defaults.CompactionThreshold).To(Equal(0.75))
			Expect(defaults.EmbeddingModel).To(Equal("nomic-embed-text"))
		})
	})

	Describe("Validate", func() {
		It("returns nil for valid manifest", func() {
			manifest := &agent.Manifest{
				ID:   "valid-agent",
				Name: "Valid Agent",
			}

			err := manifest.Validate()

			Expect(err).NotTo(HaveOccurred())
		})

		It("returns error when ID is empty", func() {
			manifest := &agent.Manifest{
				ID:   "",
				Name: "Agent Without ID",
			}

			err := manifest.Validate()

			Expect(err).To(HaveOccurred())
			var validationErr *agent.ValidationError
			Expect(err).To(BeAssignableToTypeOf(validationErr))
			Expect(err.Error()).To(ContainSubstring("id"))
			Expect(err.Error()).To(ContainSubstring("required"))
		})

		It("returns error when Name is empty", func() {
			manifest := &agent.Manifest{
				ID:   "agent-without-name",
				Name: "",
			}

			err := manifest.Validate()

			Expect(err).To(HaveOccurred())
			var validationErr *agent.ValidationError
			Expect(err).To(BeAssignableToTypeOf(validationErr))
			Expect(err.Error()).To(ContainSubstring("name"))
			Expect(err.Error()).To(ContainSubstring("required"))
		})

		It("returns error when Color is not a valid hex value", func() {
			manifest := &agent.Manifest{
				ID:    "agent-with-invalid-colour",
				Name:  "Agent With Invalid Colour",
				Color: "teal",
			}

			err := manifest.Validate()

			Expect(err).To(HaveOccurred())
			var validationErr *agent.ValidationError
			Expect(err).To(BeAssignableToTypeOf(validationErr))
			Expect(err.Error()).To(ContainSubstring("color"))
			Expect(err.Error()).To(ContainSubstring("valid hex colour"))
		})

		It("returns error when SchemaVersion contains only whitespace", func() {
			manifest := &agent.Manifest{
				SchemaVersion: "   ",
				ID:            "agent-with-blank-schema-version",
				Name:          "Agent With Blank Schema Version",
			}

			err := manifest.Validate()

			Expect(err).To(HaveOccurred())
			var validationErr *agent.ValidationError
			Expect(err).To(BeAssignableToTypeOf(validationErr))
			Expect(err.Error()).To(ContainSubstring("schema_version"))
			Expect(err.Error()).To(ContainSubstring("must not be blank"))
		})

		It("returns ID error first when both ID and Name are empty", func() {
			manifest := &agent.Manifest{
				ID:   "",
				Name: "",
			}

			err := manifest.Validate()

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("id"))
		})
	})

	// D1 (Agent Runtime Quality plan, May 2026): inherit-by-default
	// base toolset. A manifest's EffectiveTools is the union of its
	// declared capabilities.tools and the package-level
	// DefaultBaseTools, minus capabilities.tools_deny. Existing
	// manifests that omit tools_deny (the zero-valued nil slice) are
	// forward-compatible.
	Describe("EffectiveTools (D1)", func() {
		It("returns just the default base toolset for a manifest with no declared tools", func() {
			manifest := &agent.Manifest{ID: "bare", Name: "Bare"}
			Expect(manifest.EffectiveTools()).To(ConsistOf("todowrite", "todo_update", "skill_load"))
		})

		It("unions the manifest tools with the default base toolset", func() {
			manifest := &agent.Manifest{
				ID:   "with-bash",
				Name: "With Bash",
				Capabilities: agent.Capabilities{
					Tools: []string{"bash"},
				},
			}
			Expect(manifest.EffectiveTools()).To(ConsistOf("bash", "todowrite", "todo_update", "skill_load"))
		})

		It("returns a sorted slice so callers get a stable ordering", func() {
			manifest := &agent.Manifest{
				Capabilities: agent.Capabilities{Tools: []string{"write", "bash", "read"}},
			}
			tools := manifest.EffectiveTools()
			sorted := make([]string, len(tools))
			copy(sorted, tools)
			// EffectiveTools sort is alphabetical; this asserts the contract.
			Expect(sorted).To(Equal([]string{"bash", "read", "skill_load", "todo_update", "todowrite", "write"}))
		})

		It("is idempotent — declaring a base tool explicitly does not duplicate it", func() {
			manifest := &agent.Manifest{
				Capabilities: agent.Capabilities{Tools: []string{"todowrite", "bash"}},
			}
			Expect(manifest.EffectiveTools()).To(ConsistOf("todowrite", "todo_update", "skill_load", "bash"))
		})

		It("subtracts capabilities.tools_deny entries — even base tools", func() {
			manifest := &agent.Manifest{
				Capabilities: agent.Capabilities{
					Tools:     []string{"bash"},
					ToolsDeny: []string{"todowrite"},
				},
			}
			Expect(manifest.EffectiveTools()).To(ConsistOf("bash", "todo_update", "skill_load"))
		})

		It("returns the base set when called on a nil receiver", func() {
			var manifest *agent.Manifest
			Expect(manifest.EffectiveTools()).To(ConsistOf("todowrite", "todo_update", "skill_load"))
		})

		It("ignores empty string entries in capabilities.tools", func() {
			manifest := &agent.Manifest{
				Capabilities: agent.Capabilities{Tools: []string{"", "bash", ""}},
			}
			Expect(manifest.EffectiveTools()).To(ConsistOf("bash", "todowrite", "todo_update", "skill_load"))
		})

		It("survives a tools_deny that includes a tool the manifest never declared", func() {
			manifest := &agent.Manifest{
				Capabilities: agent.Capabilities{
					Tools:     []string{"bash"},
					ToolsDeny: []string{"not-in-the-set", "skill_load"},
				},
			}
			Expect(manifest.EffectiveTools()).To(ConsistOf("bash", "todowrite", "todo_update"))
		})
	})

	Describe("DefaultBaseTools (D1)", func() {
		It("exposes the three-tool floor every manifest inherits", func() {
			Expect(agent.DefaultBaseTools()).To(ConsistOf("todowrite", "todo_update", "skill_load"))
		})

		It("returns a fresh copy so callers cannot mutate the package constant", func() {
			first := agent.DefaultBaseTools()
			first[0] = "mutated"
			Expect(agent.DefaultBaseTools()).To(ConsistOf("todowrite", "todo_update", "skill_load"))
		})
	})
})

var _ = Describe("Manifest JSON deserialisation", func() {
	Context("when JSON contains a harness block", func() {
		It("deserialises HarnessConfig correctly", func() {
			raw := `{
				"id": "test-agent",
				"name": "Test Agent",
				"harness": {
					"enabled": true,
					"validators": ["schema"],
					"max_attempts": 3
				}
			}`

			var m agent.Manifest
			Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())

			Expect(m.Harness).NotTo(BeNil())
			Expect(m.Harness.Enabled).To(BeTrue())
			Expect(m.Harness.Validators).To(ConsistOf("schema"))
			Expect(m.Harness.MaxAttempts).To(Equal(3))
		})
	})

	Context("when JSON contains a loop block", func() {
		It("deserialises LoopConfig correctly", func() {
			raw := `{
				"id": "coordinator-agent",
				"name": "Coordinator Agent",
				"loop": {
					"enabled": true,
					"writer": "plan-writer",
					"reviewer": "plan-reviewer",
					"max_attempts": 3
				}
			}`

			var m agent.Manifest
			Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())

			Expect(m.Loop).NotTo(BeNil())
			Expect(m.Loop.Enabled).To(BeTrue())
			Expect(m.Loop.Writer).To(Equal("plan-writer"))
			Expect(m.Loop.Reviewer).To(Equal("plan-reviewer"))
			Expect(m.Loop.MaxAttempts).To(Equal(3))
		})
	})

	Context("when JSON contains aliases", func() {
		It("deserialises alias names correctly", func() {
			raw := `{
				"id": "research-agent",
				"name": "Research Agent",
				"aliases": ["research", "investigation"]
			}`

			var m agent.Manifest
			Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())

			Expect(m.Aliases).To(Equal([]string{"research", "investigation"}))
		})

		It("defaults aliases to an empty slice when missing", func() {
			raw := `{
				"id": "research-agent",
				"name": "Research Agent"
			}`

			var m agent.Manifest
			Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())

			Expect(m.Aliases).NotTo(BeNil())
			Expect(m.Aliases).To(BeEmpty())
		})
	})

	Context("when JSON contains neither harness nor loop blocks", func() {
		It("deserialises without error, with nil Harness and Loop fields", func() {
			raw := `{
				"id": "simple-agent",
				"name": "Simple Agent",
				"harness_enabled": true
			}`

			var m agent.Manifest
			Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())

			Expect(m.HarnessEnabled).To(BeTrue())
			Expect(m.Harness).To(BeNil())
			Expect(m.Loop).To(BeNil())
		})
	})

	Context("when JSON contains both harness_enabled and a harness block", func() {
		It("deserialises both fields independently", func() {
			raw := `{
				"id": "dual-agent",
				"name": "Dual Agent",
				"harness_enabled": true,
				"harness": {
					"enabled": true,
					"critic_enabled": true,
					"voting_enabled": true
				}
			}`

			var m agent.Manifest
			Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())

			Expect(m.HarnessEnabled).To(BeTrue())
			Expect(m.Harness).NotTo(BeNil())
			Expect(m.Harness.Enabled).To(BeTrue())
			Expect(m.Harness.CriticEnabled).To(BeTrue())
			Expect(m.Harness.VotingEnabled).To(BeTrue())
		})
	})

	Describe("Delegation struct", func() {
		It("does not have a DelegationTable field", func() {
			delegationType := reflect.TypeOf(agent.Delegation{})
			_, found := delegationType.FieldByName("DelegationTable")
			Expect(found).To(BeFalse(), "DelegationTable should be removed from Delegation struct")
		})

		// Permissive-orchestrator field (May 2026).
		//
		// `delegation.scope: permissive` lifts the active-swarm
		// Members[] / static allowlist check at the gate so designated
		// orchestrators can route across the full agent graph.
		Describe("Scope field", func() {
			It("defaults to empty (restrictive)", func() {
				d := agent.Delegation{CanDelegate: true}
				Expect(d.Scope).To(BeEmpty())
				Expect(d.IsPermissive()).To(BeFalse(),
					"empty scope must be treated as restrictive — the safe default for leaf agents")
			})

			It("IsPermissive returns true only for the permissive constant", func() {
				Expect(agent.Delegation{Scope: agent.DelegationScopePermissive}.IsPermissive()).To(BeTrue())
				Expect(agent.Delegation{Scope: agent.DelegationScopeRestrictive}.IsPermissive()).To(BeFalse())
				Expect(agent.Delegation{Scope: ""}.IsPermissive()).To(BeFalse())
				Expect(agent.Delegation{Scope: "PERMISSIVE"}.IsPermissive()).To(BeFalse(),
					"value is case-sensitive — typos must NOT silently widen scope")
			})

			It("deserialises from JSON via delegation.scope", func() {
				raw := `{
					"id": "coord",
					"name": "Coord",
					"delegation": {
						"can_delegate": true,
						"delegation_allowlist": [],
						"scope": "permissive"
					}
				}`
				var m agent.Manifest
				Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())
				Expect(m.Delegation.Scope).To(Equal("permissive"))
				Expect(m.Delegation.IsPermissive()).To(BeTrue())
			})

			It("deserialises from JSON omitting scope as empty (restrictive)", func() {
				raw := `{
					"id": "leaf",
					"name": "Leaf",
					"delegation": {"can_delegate": true, "delegation_allowlist": []}
				}`
				var m agent.Manifest
				Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())
				Expect(m.Delegation.Scope).To(BeEmpty())
				Expect(m.Delegation.IsPermissive()).To(BeFalse())
			})
		})

		Describe("Validate scope field", func() {
			It("accepts empty scope", func() {
				m := &agent.Manifest{ID: "x", Name: "X"}
				Expect(m.Validate()).To(Succeed())
			})

			It("accepts permissive and restrictive", func() {
				perm := &agent.Manifest{ID: "x", Name: "X", Delegation: agent.Delegation{Scope: "permissive"}}
				rest := &agent.Manifest{ID: "x", Name: "X", Delegation: agent.Delegation{Scope: "restrictive"}}
				Expect(perm.Validate()).To(Succeed())
				Expect(rest.Validate()).To(Succeed())
			})

			It("rejects unknown scope values", func() {
				m := &agent.Manifest{ID: "x", Name: "X", Delegation: agent.Delegation{Scope: "open"}}
				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("delegation.scope"))
			})
		})
	})

	Describe("Model preferences", func() {
		Context("when JSON declares preferred_models and model_policy", func() {
			It("deserialises both fields", func() {
				raw := `{
					"id": "junior",
					"name": "Junior Engineer",
					"preferred_models": [
						{"provider": "anthropic", "model": "claude-haiku-4"},
						{"provider": "anthropic", "model": "claude-sonnet-4"}
					],
					"model_policy": "strict"
				}`

				var m agent.Manifest
				Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())

				Expect(m.ModelPolicy).To(Equal("strict"))
				Expect(m.PreferredModels).To(HaveLen(2))
				Expect(m.PreferredModels[0].Provider).To(Equal("anthropic"))
				Expect(m.PreferredModels[0].Model).To(Equal("claude-haiku-4"))
				Expect(m.PreferredModels[1].Model).To(Equal("claude-sonnet-4"))
			})
		})

		Describe("IsModelAllowed", func() {
			It("allows any model when policy is empty", func() {
				m := agent.Manifest{ID: "x", Name: "X"}

				Expect(m.IsModelAllowed("anthropic", "anything")).To(BeTrue())
			})

			It("allows any model under permissive policy regardless of preferences", func() {
				m := agent.Manifest{
					ID:          "senior",
					Name:        "Senior",
					ModelPolicy: "permissive",
					PreferredModels: []agent.ModelPreference{
						{Provider: "anthropic", Model: "claude-opus-4"},
					},
				}

				Expect(m.IsModelAllowed("anthropic", "claude-opus-4")).To(BeTrue())
				Expect(m.IsModelAllowed("openai", "gpt-4o")).To(BeTrue())
			})

			It("rejects non-listed models under strict policy", func() {
				m := agent.Manifest{
					ID:          "junior",
					Name:        "Junior",
					ModelPolicy: "strict",
					PreferredModels: []agent.ModelPreference{
						{Provider: "anthropic", Model: "claude-haiku-4"},
					},
				}

				Expect(m.IsModelAllowed("anthropic", "claude-haiku-4")).To(BeTrue())
				Expect(m.IsModelAllowed("anthropic", "claude-opus-4")).To(BeFalse())
				Expect(m.IsModelAllowed("openai", "gpt-4o")).To(BeFalse())
			})

			It("falls back to permissive when strict is set without preferences", func() {
				m := agent.Manifest{
					ID:              "edge",
					Name:            "Edge",
					ModelPolicy:     "strict",
					PreferredModels: nil,
				}

				Expect(m.IsModelAllowed("openai", "gpt-4o")).To(BeTrue(),
					"strict + empty list is meaningless and must not lock the user out")
			})
		})

		Describe("IsModelPreferred", func() {
			It("identifies preferred models regardless of policy", func() {
				m := agent.Manifest{
					ID:          "senior",
					Name:        "Senior",
					ModelPolicy: "permissive",
					PreferredModels: []agent.ModelPreference{
						{Provider: "anthropic", Model: "claude-opus-4"},
					},
				}

				Expect(m.IsModelPreferred("anthropic", "claude-opus-4")).To(BeTrue())
				Expect(m.IsModelPreferred("openai", "gpt-4o")).To(BeFalse())
			})
		})

		Describe("Validate", func() {
			It("rejects unknown model_policy values", func() {
				m := &agent.Manifest{
					ID:          "bad",
					Name:        "Bad",
					ModelPolicy: "tyrannical",
				}

				err := m.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("model_policy"))
			})

			It("accepts the empty model_policy", func() {
				m := &agent.Manifest{ID: "ok", Name: "OK", ModelPolicy: ""}
				Expect(m.Validate()).To(Succeed())
			})

			It("accepts strict and permissive", func() {
				strict := &agent.Manifest{ID: "ok", Name: "OK", ModelPolicy: "strict"}
				permissive := &agent.Manifest{ID: "ok", Name: "OK", ModelPolicy: "permissive"}
				Expect(strict.Validate()).To(Succeed())
				Expect(permissive.Validate()).To(Succeed())
			})
		})
	})
})

var _ = Describe("ValidationError", func() {
	Describe("Error", func() {
		It("formats error message correctly", func() {
			err := &agent.ValidationError{
				Field:   "test_field",
				Message: "test message",
			}

			Expect(err.Error()).To(Equal("test_field: test message"))
		})
	})
})

// D3 (Agent Runtime Quality plan, May 2026): Capabilities gains the
// sibling key tools_deny. New manifests opt in by listing tool names;
// pre-D3 manifests have the zero-valued nil slice and are fully
// forward-compatible.
var _ = Describe("Capabilities.ToolsDeny (D3) parsing", func() {
	Context("JSON deserialisation", func() {
		It("parses capabilities.tools_deny from JSON", func() {
			raw := `{"id":"x","name":"X","capabilities":{"tools":["bash"],"tools_deny":["todowrite"]}}`
			var m agent.Manifest
			Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())
			Expect(m.Capabilities.ToolsDeny).To(ConsistOf("todowrite"))
		})

		It("defaults to an empty slice when tools_deny is omitted (backward compat)", func() {
			raw := `{"id":"x","name":"X","capabilities":{"tools":["bash"]}}`
			var m agent.Manifest
			Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())
			Expect(m.Capabilities.ToolsDeny).To(BeEmpty())
		})
	})
})
