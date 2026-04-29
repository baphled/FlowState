package slashcommand

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

// arStep enumerates the wizard's state-machine positions.
type arStep int

const (
	arStepPreset arStep = iota
	arStepSurface
	arStepEvaluator
	arStepDriver
	arStepMetricDirection
	arStepMaxTrials
	arStepTimeBudget
	arStepConfirm
	arStepDone
)

// arPreset holds the values pre-filled by a named preset.
type arPreset struct {
	evaluator string
	driver    string
	program   string
	direction string
}

const (
	arPresetPlannerQuality        = "planner-quality"
	arPresetPerfPreserveBehaviour = "perf-preserve-behaviour"
	arPresetCustom                = "custom"
)

var arPresets = map[string]arPreset{
	arPresetPlannerQuality: {
		evaluator: "scripts/autoresearch-evaluators/planner-validate.sh",
		driver:    "scripts/autoresearch-drivers/default-assistant-driver.sh",
		program:   "planner-quality",
		direction: "min",
	},
	arPresetPerfPreserveBehaviour: {
		evaluator: "scripts/autoresearch-evaluators/bench.sh",
		driver:    "scripts/autoresearch-drivers/default-assistant-driver.sh",
		program:   "skills/autoresearch-presets/perf-preserve-behaviour.md",
		direction: "min",
	},
}

const (
	arDefaultDriver    = "scripts/autoresearch-drivers/default-assistant-driver.sh"
	arDefaultMaxTrials = "10"
	arDefaultBudget    = "5m"
)

// autoresearchBuilder is the multi-step state machine implementing Wizard
// for /autoresearch. It assembles a flowstate autoresearch run command and
// injects it into the chat session via MessageSender.
type autoresearchBuilder struct {
	step              arStep
	preset            string
	surface           string
	evaluator         string
	evaluatorDefault  string
	driver            string
	driverDefault     string
	direction         string
	directionDefault  string
	program           string
	maxTrials         string
	timeBudget        string
	sender            MessageSender
	cancelled         bool
	completionMessage string
}

// NewAutoresearchBuilder constructs the /autoresearch wizard.
//
// Expected:
//   - sender may be nil; the wizard still assembles the command but
//     will not inject it as a chat message on confirm.
//
// Returns:
//   - A Wizard ready for the chat intent to drive.
//
// Side effects:
//   - None.
func NewAutoresearchBuilder(sender MessageSender) Wizard {
	return &autoresearchBuilder{
		step:   arStepPreset,
		sender: sender,
	}
}

// Current renders the wizard's current step.
//
// Returns:
//   - The active WizardStep.
//
// Side effects:
//   - None.
func (b *autoresearchBuilder) Current() WizardStep {
	switch b.step {
	case arStepPreset:
		return WizardStep{
			Kind:   StepPicker,
			Prompt: "Optimisation preset:",
			Items: []widgets.Item{
				{Label: "Planner quality (min warnings)", Value: arPresetPlannerQuality},
				{Label: "Performance (min ns/op)", Value: arPresetPerfPreserveBehaviour},
				{Label: "Custom", Value: arPresetCustom},
			},
		}
	case arStepSurface:
		return WizardStep{
			Kind:   StepInput,
			Prompt: "Surface file path (relative to repo root):",
		}
	case arStepEvaluator:
		return WizardStep{
			Kind:   StepInput,
			Prompt: fmt.Sprintf("Evaluator script path [default: %s]:", b.evaluatorDefault),
		}
	case arStepDriver:
		return WizardStep{
			Kind:   StepInput,
			Prompt: fmt.Sprintf("Driver script path [default: %s]:", b.driverDefault),
		}
	case arStepMetricDirection:
		items := b.directionItems()
		return WizardStep{
			Kind:   StepPicker,
			Prompt: "Metric direction:",
			Items:  items,
		}
	case arStepMaxTrials:
		return WizardStep{
			Kind:   StepInput,
			Prompt: fmt.Sprintf("Max trials [default: %s]:", arDefaultMaxTrials),
		}
	case arStepTimeBudget:
		return WizardStep{
			Kind:   StepInput,
			Prompt: fmt.Sprintf("Time budget [default: %s]:", arDefaultBudget),
		}
	case arStepConfirm:
		cmd := b.assembleCommand()
		return WizardStep{
			Kind:           StepConfirm,
			Prompt:         "Launch autoresearch run?",
			Items:          yesNoItems(),
			PreviewMessage: cmd,
		}
	}
	return WizardStep{Kind: StepDone}
}

// directionItems returns metric-direction picker items, placing the preset
// direction first so it appears pre-selected.
func (b *autoresearchBuilder) directionItems() []widgets.Item {
	min := widgets.Item{Label: "min (lower is better)", Value: "min"}
	max := widgets.Item{Label: "max (higher is better)", Value: "max"}
	if b.directionDefault == "max" {
		return []widgets.Item{max, min}
	}
	return []widgets.Item{min, max}
}

// SubmitText advances a text-input step.
//
// Returns:
//   - nil when the input is acceptable.
//   - An error to keep the same step open with the error surfaced as a
//     system message.
//
// Side effects:
//   - May advance b.step.
func (b *autoresearchBuilder) SubmitText(value string) error {
	value = strings.TrimSpace(value)
	switch b.step {
	case arStepSurface:
		if value == "" {
			return errors.New("surface file path is required")
		}
		b.surface = value
		b.step = arStepEvaluator
		return nil
	case arStepEvaluator:
		if value == "" {
			value = b.evaluatorDefault
		}
		b.evaluator = value
		b.step = arStepDriver
		return nil
	case arStepDriver:
		if value == "" {
			value = b.driverDefault
		}
		b.driver = value
		b.step = arStepMetricDirection
		return nil
	case arStepMaxTrials:
		if value == "" {
			value = arDefaultMaxTrials
		}
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return fmt.Errorf("max trials must be a positive integer, got %q", value)
		}
		b.maxTrials = value
		b.step = arStepTimeBudget
		return nil
	case arStepTimeBudget:
		if value == "" {
			value = arDefaultBudget
		}
		b.timeBudget = value
		b.step = arStepConfirm
		return nil
	}
	return fmt.Errorf("unexpected text submission at step %d", b.step)
}

// SubmitItem advances a single-select picker or confirm step.
//
// Returns:
//   - nil on success.
//   - An error to keep the same step open.
//
// Side effects:
//   - May advance b.step or set b.completionMessage.
func (b *autoresearchBuilder) SubmitItem(item widgets.Item) error {
	switch b.step {
	case arStepPreset:
		return b.submitPreset(item)
	case arStepMetricDirection:
		dir, _ := item.Value.(string)
		if dir == "" {
			dir = b.directionDefault
		}
		b.direction = dir
		b.step = arStepMaxTrials
		return nil
	case arStepConfirm:
		return b.submitConfirm(item)
	}
	return fmt.Errorf("unexpected item submission at step %d", b.step)
}

// submitPreset applies a preset selection and pre-fills defaults.
func (b *autoresearchBuilder) submitPreset(item widgets.Item) error {
	key, _ := item.Value.(string)
	b.preset = key
	if p, ok := arPresets[key]; ok {
		b.evaluatorDefault = p.evaluator
		b.driverDefault = p.driver
		b.program = p.program
		b.directionDefault = p.direction
	} else {
		b.evaluatorDefault = ""
		b.driverDefault = arDefaultDriver
		b.directionDefault = "min"
	}
	b.step = arStepSurface
	return nil
}

// submitConfirm handles the final yes/no step.
func (b *autoresearchBuilder) submitConfirm(item widgets.Item) error {
	v, _ := item.Value.(string)
	if v != confirmYes {
		b.cancelled = true
		b.step = arStepDone
		return nil
	}
	cmd := b.assembleCommand()
	b.completionMessage = cmd
	if b.sender != nil {
		b.sender.SendUserMessage(cmd)
	}
	b.step = arStepDone
	return nil
}

// SubmitMulti is not used by this wizard; returns an error for any call.
func (b *autoresearchBuilder) SubmitMulti(_ []widgets.Item) error {
	return fmt.Errorf("multi-select is not supported by the autoresearch wizard")
}

// Cancel marks the wizard as cancelled. Idempotent.
//
// Side effects:
//   - Sets b.cancelled = true.
func (b *autoresearchBuilder) Cancel() {
	b.cancelled = true
	b.completionMessage = ""
}

// CompleteMessage returns the assembled command on the happy path, or
// empty when the wizard was cancelled or "no" was chosen at confirm.
//
// Returns:
//   - The assembled flowstate autoresearch run ... command, or "".
//
// Side effects:
//   - None.
func (b *autoresearchBuilder) CompleteMessage() string {
	if b.cancelled {
		return ""
	}
	return b.completionMessage
}

// assembleCommand builds the flowstate autoresearch run command string
// from the collected inputs.
func (b *autoresearchBuilder) assembleCommand() string {
	var parts []string
	parts = append(parts, "flowstate autoresearch run")
	parts = append(parts, "  --surface "+b.surface)
	parts = append(parts, "  --evaluator-script "+b.evaluator)
	parts = append(parts, "  --driver-script "+b.driver)
	parts = append(parts, "  --metric-direction "+b.direction)
	if b.program != "" {
		parts = append(parts, "  --program "+b.program)
	}
	if b.maxTrials != "" && b.maxTrials != arDefaultMaxTrials {
		parts = append(parts, "  --max-trials "+b.maxTrials)
	}
	if b.timeBudget != "" && b.timeBudget != arDefaultBudget {
		parts = append(parts, "  --time-budget "+b.timeBudget)
	}
	return strings.Join(parts, " \\\n")
}
