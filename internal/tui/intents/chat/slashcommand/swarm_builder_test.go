package slashcommand_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tui/intents/chat/slashcommand"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("SwarmBuilder", func() {
	var (
		dir     string
		agents  *agent.Registry
		schemas []string
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		agents = newSwarmAgentRegistry()
		schemas = []string{"review-verdict-v1", "evidence-bundle-v1"}
	})

	Describe("happy path", func() {
		It("walks every step and writes a manifest to disk", func() {
			wizard := slashcommand.NewSwarmBuilder(agents, schemas, dir)

			expectStep(wizard, slashcommand.StepInput)
			Expect(wizard.SubmitText("my-swarm")).To(Succeed())

			expectStep(wizard, slashcommand.StepPicker)
			Expect(wizard.SubmitItem(itemFor("planner"))).To(Succeed())

			expectStep(wizard, slashcommand.StepMultiPicker)
			Expect(wizard.SubmitMulti([]widgets.Item{itemFor("explorer")})).To(Succeed())

			expectStep(wizard, slashcommand.StepPicker)
			Expect(wizard.SubmitItem(itemFor("no"))).To(Succeed())

			expectStep(wizard, slashcommand.StepConfirm)
			Expect(wizard.SubmitItem(itemFor("yes"))).To(Succeed())

			Expect(wizard.Current().Kind).To(Equal(slashcommand.StepDone))

			path := filepath.Join(dir, "my-swarm.yml")
			body, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())

			manifest := unmarshalManifest(body)
			Expect(manifest.SchemaVersion).To(Equal(swarm.SchemaVersionV1))
			Expect(manifest.ID).To(Equal("my-swarm"))
			Expect(manifest.Lead).To(Equal("planner"))
			Expect(manifest.Members).To(Equal([]string{"explorer"}))
			Expect(manifest.Context.ChainPrefix).To(Equal("my-swarm"))
		})
	})

	Describe("name validation", func() {
		It("rejects empty names", func() {
			wizard := slashcommand.NewSwarmBuilder(agents, schemas, dir)
			err := wizard.SubmitText("   ")
			Expect(err).To(HaveOccurred())
			Expect(wizard.Current().Kind).To(Equal(slashcommand.StepInput))
		})

		It("rejects names with path separators", func() {
			wizard := slashcommand.NewSwarmBuilder(agents, schemas, dir)
			Expect(wizard.SubmitText("foo/bar")).To(HaveOccurred())
		})
	})

	Describe("overwrite confirmation", func() {
		It("prompts before overwriting an existing manifest", func() {
			seedSwarmManifest(dir, "existing")
			wizard := slashcommand.NewSwarmBuilder(agents, schemas, dir)

			Expect(wizard.SubmitText("existing")).To(Succeed())
			Expect(wizard.Current().Kind).To(Equal(slashcommand.StepPicker))
			Expect(wizard.Current().Prompt).To(ContainSubstring("overwrite"))
		})

		It("cancels when the user declines overwrite", func() {
			seedSwarmManifest(dir, "existing")
			wizard := slashcommand.NewSwarmBuilder(agents, schemas, dir)

			Expect(wizard.SubmitText("existing")).To(Succeed())
			Expect(wizard.SubmitItem(itemFor("no"))).To(Succeed())
			Expect(wizard.Current().Kind).To(Equal(slashcommand.StepDone))
			Expect(wizard.CompleteMessage()).To(BeEmpty())
		})
	})

	Describe("members multi-select", func() {
		It("filters out the lead from the candidate list", func() {
			wizard := slashcommand.NewSwarmBuilder(agents, schemas, dir)
			Expect(wizard.SubmitText("s")).To(Succeed())
			Expect(wizard.SubmitItem(itemFor("planner"))).To(Succeed())

			labels := stepLabels(wizard.Current().Items)
			Expect(labels).NotTo(ContainElement("planner"))
			Expect(labels).To(ContainElements("explorer", "executor"))
		})
	})

	Describe("gate addition loop", func() {
		It("collects a builtin:result-schema gate then loops", func() {
			wizard := slashcommand.NewSwarmBuilder(agents, schemas, dir)
			driveToGateLoop(wizard)

			Expect(wizard.SubmitItem(itemFor("yes"))).To(Succeed())
			Expect(wizard.SubmitText("post-explorer-bundle")).To(Succeed())
			Expect(wizard.SubmitItem(itemFor("builtin:result-schema"))).To(Succeed())
			Expect(wizard.SubmitItem(itemFor(swarm.LifecyclePostMember))).To(Succeed())
			Expect(wizard.SubmitItem(itemFor("explorer"))).To(Succeed())
			Expect(wizard.SubmitItem(itemFor("evidence-bundle-v1"))).To(Succeed())

			Expect(wizard.Current().Prompt).To(Equal("Add a gate?"))
			Expect(wizard.SubmitItem(itemFor("no"))).To(Succeed())
			Expect(wizard.SubmitItem(itemFor("yes"))).To(Succeed())

			body := readManifest(dir, "ws")
			manifest := unmarshalManifest(body)
			Expect(manifest.Harness.Gates).To(HaveLen(1))
			Expect(manifest.Harness.Gates[0].Name).To(Equal("post-explorer-bundle"))
			Expect(manifest.Harness.Gates[0].SchemaRef).To(Equal("evidence-bundle-v1"))
			Expect(manifest.Harness.Gates[0].Target).To(Equal("explorer"))
		})
	})

	Describe("Cancel", func() {
		It("removes the manifest when cancelled mid-write", func() {
			wizard := slashcommand.NewSwarmBuilder(agents, schemas, dir)
			driveToGateLoop(wizard)
			Expect(wizard.SubmitItem(itemFor("no"))).To(Succeed())
			Expect(wizard.SubmitItem(itemFor("yes"))).To(Succeed())

			path := filepath.Join(dir, "ws.yml")
			Expect(path).To(BeARegularFile())

			wizard.Cancel()
			Expect(path).NotTo(BeARegularFile())
		})
	})

	Describe("schema picker", func() {
		It("renders a placeholder when no schemas are registered", func() {
			wizard := slashcommand.NewSwarmBuilder(agents, nil, dir)
			driveToGateSchema(wizard)
			Expect(stepLabels(wizard.Current().Items)).To(ContainElement("(no schemas registered)"))
		})
	})
})

var _ = Describe("autoresearch wizard", func() {
	Describe("happy path — planner-quality preset", func() {
		It("walks all 8 steps and produces an assembled command", func() {
			sender := &fakeSender{}
			wizard := slashcommand.NewAutoresearchBuilder(sender)

			expectStep(wizard, slashcommand.StepPicker)
			Expect(wizard.SubmitItem(itemFor("planner-quality"))).To(Succeed())

			expectStep(wizard, slashcommand.StepInput)
			Expect(wizard.SubmitText("internal/app/agents/planner.md")).To(Succeed())

			expectStep(wizard, slashcommand.StepInput)
			Expect(wizard.SubmitText("")).To(Succeed())

			expectStep(wizard, slashcommand.StepInput)
			Expect(wizard.SubmitText("")).To(Succeed())

			expectStep(wizard, slashcommand.StepPicker)
			Expect(wizard.SubmitItem(itemFor("min"))).To(Succeed())

			expectStep(wizard, slashcommand.StepInput)
			Expect(wizard.SubmitText("")).To(Succeed())

			expectStep(wizard, slashcommand.StepInput)
			Expect(wizard.SubmitText("")).To(Succeed())

			expectStep(wizard, slashcommand.StepConfirm)
			Expect(wizard.SubmitItem(itemFor("yes"))).To(Succeed())

			Expect(wizard.Current().Kind).To(Equal(slashcommand.StepDone))

			msg := wizard.CompleteMessage()
			Expect(msg).To(ContainSubstring("flowstate autoresearch run"))
			Expect(msg).To(ContainSubstring("--surface internal/app/agents/planner.md"))
			Expect(msg).To(ContainSubstring("--evaluator-script scripts/autoresearch-evaluators/planner-validate.sh"))
			Expect(msg).To(ContainSubstring("--driver-script scripts/autoresearch-drivers/default-assistant-driver.sh"))
			Expect(msg).To(ContainSubstring("--metric-direction min"))
			Expect(msg).To(ContainSubstring("--program planner-quality"))

			Expect(sender.received).To(Equal(msg))
		})
	})

	Describe("preset pre-fill", func() {
		It("planner-quality pre-fills evaluator prompt with planner-validate.sh", func() {
			wizard := slashcommand.NewAutoresearchBuilder(nil)
			Expect(wizard.SubmitItem(itemFor("planner-quality"))).To(Succeed())
			Expect(wizard.SubmitText("internal/app/agents/planner.md")).To(Succeed())

			step := wizard.Current()
			Expect(step.Prompt).To(ContainSubstring("planner-validate.sh"))
		})

		It("perf-preserve-behaviour pre-fills evaluator prompt with bench.sh", func() {
			wizard := slashcommand.NewAutoresearchBuilder(nil)
			Expect(wizard.SubmitItem(itemFor("perf-preserve-behaviour"))).To(Succeed())
			Expect(wizard.SubmitText("internal/engine/hot_path.go")).To(Succeed())

			step := wizard.Current()
			Expect(step.Prompt).To(ContainSubstring("bench.sh"))
		})

		It("custom preset leaves evaluator default empty in prompt", func() {
			wizard := slashcommand.NewAutoresearchBuilder(nil)
			Expect(wizard.SubmitItem(itemFor("custom"))).To(Succeed())

			Expect(wizard.SubmitText("my-surface.go")).To(Succeed())
			step := wizard.Current()
			Expect(step.Prompt).To(ContainSubstring("Evaluator script path"))
		})
	})

	Describe("input validation", func() {
		It("rejects empty surface", func() {
			wizard := slashcommand.NewAutoresearchBuilder(nil)
			Expect(wizard.SubmitItem(itemFor("planner-quality"))).To(Succeed())

			err := wizard.SubmitText("   ")
			Expect(err).To(HaveOccurred())
			expectStep(wizard, slashcommand.StepInput)
		})

		It("rejects non-numeric max trials", func() {
			wizard := slashcommand.NewAutoresearchBuilder(nil)
			driveToMaxTrials(wizard)

			err := wizard.SubmitText("not-a-number")
			Expect(err).To(HaveOccurred())
			expectStep(wizard, slashcommand.StepInput)
		})

		It("rejects zero max trials", func() {
			wizard := slashcommand.NewAutoresearchBuilder(nil)
			driveToMaxTrials(wizard)

			err := wizard.SubmitText("0")
			Expect(err).To(HaveOccurred())
			expectStep(wizard, slashcommand.StepInput)
		})
	})

	Describe("cancel / decline", func() {
		It("CompleteMessage is empty after Cancel()", func() {
			wizard := slashcommand.NewAutoresearchBuilder(nil)
			Expect(wizard.SubmitItem(itemFor("planner-quality"))).To(Succeed())
			Expect(wizard.SubmitText("some/surface.md")).To(Succeed())

			wizard.Cancel()
			Expect(wizard.CompleteMessage()).To(BeEmpty())
		})

		It("CompleteMessage is empty when user declines confirm", func() {
			wizard := slashcommand.NewAutoresearchBuilder(nil)
			driveToConfirm(wizard)

			Expect(wizard.SubmitItem(itemFor("no"))).To(Succeed())
			Expect(wizard.Current().Kind).To(Equal(slashcommand.StepDone))
			Expect(wizard.CompleteMessage()).To(BeEmpty())
		})

		It("nil sender does not panic on confirm", func() {
			wizard := slashcommand.NewAutoresearchBuilder(nil)
			driveToConfirm(wizard)

			Expect(wizard.SubmitItem(itemFor("yes"))).To(Succeed())
			Expect(wizard.CompleteMessage()).NotTo(BeEmpty())
		})
	})
})

// fakeSender captures the last SendUserMessage call for assertion.
type fakeSender struct {
	received string
}

func (f *fakeSender) SendUserMessage(text string) {
	f.received = text
}

// driveToMaxTrials advances an autoresearch wizard to the max-trials step
// using the planner-quality preset and a fixed surface.
func driveToMaxTrials(wizard slashcommand.Wizard) {
	Expect(wizard.SubmitItem(itemFor("planner-quality"))).To(Succeed())
	Expect(wizard.SubmitText("internal/app/agents/planner.md")).To(Succeed())
	Expect(wizard.SubmitText("")).To(Succeed())
	Expect(wizard.SubmitText("")).To(Succeed())
	Expect(wizard.SubmitItem(itemFor("min"))).To(Succeed())
}

// driveToConfirm drives an autoresearch wizard to the confirm step.
func driveToConfirm(wizard slashcommand.Wizard) {
	driveToMaxTrials(wizard)
	Expect(wizard.SubmitText("")).To(Succeed())
	Expect(wizard.SubmitText("")).To(Succeed())
}

func newSwarmAgentRegistry() *agent.Registry {
	reg := agent.NewRegistry()
	reg.Register(&agent.Manifest{ID: "planner", Name: "Planner", Mode: "plan"})
	reg.Register(&agent.Manifest{ID: "executor", Name: "Executor", Mode: "execute"})
	reg.Register(&agent.Manifest{ID: "explorer", Name: "Explorer"})
	return reg
}

func seedSwarmManifest(dir, name string) {
	path := filepath.Join(dir, name+".yml")
	Expect(os.WriteFile(path, []byte("seed"), 0o644)).To(Succeed())
}

func itemFor(value string) widgets.Item {
	return widgets.Item{Label: value, Value: value}
}

func expectStep(wizard slashcommand.Wizard, kind slashcommand.WizardStepKind) {
	Expect(wizard.Current().Kind).To(Equal(kind))
}

func stepLabels(items []widgets.Item) []string {
	out := make([]string, len(items))
	for idx, item := range items {
		out[idx] = item.Label
	}
	return out
}

func unmarshalManifest(body []byte) swarm.Manifest {
	var m swarm.Manifest
	Expect(yaml.Unmarshal(body, &m)).To(Succeed())
	return m
}

func readManifest(dir, name string) []byte {
	body, err := os.ReadFile(filepath.Join(dir, name+".yml"))
	Expect(err).NotTo(HaveOccurred())
	return body
}

func driveToGateLoop(wizard slashcommand.Wizard) {
	Expect(wizard.SubmitText("ws")).To(Succeed())
	Expect(wizard.SubmitItem(itemFor("planner"))).To(Succeed())
	Expect(wizard.SubmitMulti([]widgets.Item{itemFor("explorer")})).To(Succeed())
}

func driveToGateSchema(wizard slashcommand.Wizard) {
	driveToGateLoop(wizard)
	Expect(wizard.SubmitItem(itemFor("yes"))).To(Succeed())
	Expect(wizard.SubmitText("g")).To(Succeed())
	Expect(wizard.SubmitItem(itemFor("builtin:result-schema"))).To(Succeed())
	Expect(wizard.SubmitItem(itemFor(swarm.LifecyclePostSwarm))).To(Succeed())
}
