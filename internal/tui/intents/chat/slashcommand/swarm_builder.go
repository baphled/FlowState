package slashcommand

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

// builderStep enumerates the wizard's state-machine positions. Pulled
// out as a typed enum so the swarm_builder transitions stay readable
// against a single switch.
type builderStep int

const (
	stepName builderStep = iota
	stepConfirmOverwrite
	stepLead
	stepMembers
	stepAskAddGate
	stepGateName
	stepGateKind
	stepGateWhen
	stepGateTarget
	stepGateSchema
	stepConfirmWrite
	stepFinished
)

// confirmYes / confirmNo are the opaque values stored on confirmation
// picker items so swarm_builder can branch without inspecting label
// strings.
const (
	confirmYes = "yes"
	confirmNo  = "no"
)

// gateKindResultSchema is the only kind the Phase 1 wizard surfaces.
// ext:* gates require an Extension API v1 backend that has not landed
// yet; once it does, expand the gate-kind picker.
const gateKindResultSchema = "builtin:result-schema"

// swarmBuilder is the multi-step state machine implementing Wizard for
// /swarm. The exported NewSwarmBuilder constructor is what the /swarm
// command's OpenWizard calls.
type swarmBuilder struct {
	step              builderStep
	name              string
	leadID            string
	memberIDs         []string
	gates             []swarm.GateSpec
	pendingGate       swarm.GateSpec
	chainPrefix       string
	agents            *agent.Registry
	schemaNames       []string
	swarmsDir         string
	overwrite         bool
	cancelled         bool
	wroteFile         bool
	completionMessage string
}

// NewSwarmBuilder constructs the /swarm wizard. The caller wires up
// the agent registry, the schema-name list, and the swarms directory
// so the builder stays decoupled from concrete config plumbing — the
// chat intent's CommandContext composes the wiring.
//
// Expected:
//   - agents is the agent.Registry consulted for the lead and members
//     pickers; required (a nil registry would yield an empty lead pick
//     list and the wizard cannot recover).
//   - schemaNames is the snapshot of registered SchemaRef names from
//     swarm.RegisteredSchemaNames(); empty means the schema picker step
//     is skipped (the gate is recorded with a blank SchemaRef).
//   - swarmsDir is the destination directory for manifest writes
//     (typically "$HOME/.config/flowstate/swarms").
//
// Returns:
//   - A Wizard ready for the chat intent to drive.
//
// Side effects:
//   - None (no filesystem touches until the user confirms the write).
func NewSwarmBuilder(agents *agent.Registry, schemaNames []string, swarmsDir string) Wizard {
	return &swarmBuilder{
		step:        stepName,
		agents:      agents,
		schemaNames: append([]string(nil), schemaNames...),
		swarmsDir:   swarmsDir,
	}
}

// Current renders the wizard's current step. The chat intent inspects
// the returned WizardStep to decide which surface to show.
//
// Returns:
//   - The active WizardStep.
//
// Side effects:
//   - None.
func (b *swarmBuilder) Current() WizardStep {
	switch b.step {
	case stepName:
		return WizardStep{Kind: StepInput, Prompt: "Swarm name (id):"}
	case stepConfirmOverwrite:
		return WizardStep{
			Kind:   StepPicker,
			Prompt: fmt.Sprintf("Manifest %q already exists — overwrite?", b.name),
			Items:  yesNoItems(),
		}
	case stepLead:
		return WizardStep{
			Kind:   StepPicker,
			Prompt: "Pick the lead agent:",
			Items:  b.agentItems(),
		}
	case stepMembers:
		return WizardStep{
			Kind:   StepMultiPicker,
			Prompt: "Toggle members (Space) — Enter when done:",
			Items:  b.memberCandidateItems(),
		}
	case stepAskAddGate:
		return WizardStep{
			Kind:   StepPicker,
			Prompt: "Add a gate?",
			Items:  yesNoItems(),
		}
	case stepGateName:
		return WizardStep{Kind: StepInput, Prompt: "Gate name:"}
	case stepGateKind:
		return WizardStep{
			Kind:   StepPicker,
			Prompt: "Gate kind:",
			Items:  []widgets.Item{{Label: gateKindResultSchema, Value: gateKindResultSchema}},
		}
	case stepGateWhen:
		return WizardStep{
			Kind:   StepPicker,
			Prompt: "When does the gate fire?",
			Items:  whenItems(),
		}
	case stepGateTarget:
		return WizardStep{
			Kind:   StepPicker,
			Prompt: "Gate target (member id, or 'none' for swarm-level):",
			Items:  b.gateTargetItems(),
		}
	case stepGateSchema:
		return WizardStep{
			Kind:   StepPicker,
			Prompt: "Schema reference for the gate:",
			Items:  b.schemaItems(),
		}
	case stepConfirmWrite:
		return WizardStep{
			Kind:           StepConfirm,
			Prompt:         fmt.Sprintf("Write %s.yml?", b.name),
			Items:          yesNoItems(),
			PreviewMessage: b.renderPreview(),
		}
	}
	return WizardStep{Kind: StepDone}
}

// SubmitText advances a text-input step.
//
// Expected:
//   - value is the user-typed string; whitespace handling is per step.
//
// Returns:
//   - nil when the input is acceptable; an error to keep the same step
//     open with the error surfaced as a system message.
//
// Side effects:
//   - May advance b.step.
func (b *swarmBuilder) SubmitText(value string) error {
	value = strings.TrimSpace(value)
	switch b.step {
	case stepName:
		return b.submitName(value)
	case stepGateName:
		if value == "" {
			return errors.New("gate name is required")
		}
		b.pendingGate = swarm.GateSpec{Name: value}
		b.step = stepGateKind
		return nil
	}
	return fmt.Errorf("unexpected text submission at step %d", b.step)
}

// SubmitItem advances a single-select picker step.
//
// Expected:
//   - item is the picker.Item the user chose.
//
// Returns:
//   - nil on success; an error to keep the same step open.
//
// Side effects:
//   - May advance b.step.
func (b *swarmBuilder) SubmitItem(item widgets.Item) error {
	switch b.step {
	case stepConfirmOverwrite:
		return b.submitOverwriteChoice(item)
	case stepLead:
		return b.submitLeadChoice(item)
	case stepAskAddGate:
		return b.submitAddGateChoice(item)
	case stepGateKind:
		return b.submitGateKindChoice(item)
	case stepGateWhen:
		return b.submitGateWhenChoice(item)
	case stepGateTarget:
		return b.submitGateTargetChoice(item)
	case stepGateSchema:
		return b.submitGateSchemaChoice(item)
	case stepConfirmWrite:
		return b.submitConfirmWriteChoice(item)
	}
	return fmt.Errorf("unexpected item submission at step %d", b.step)
}

// SubmitMulti advances a multi-picker step.
//
// Expected:
//   - items is the multi-set committed by the picker.
//
// Returns:
//   - nil on success; an error to keep the step open on validation
//     failures.
//
// Side effects:
//   - May advance b.step.
func (b *swarmBuilder) SubmitMulti(items []widgets.Item) error {
	if b.step != stepMembers {
		return fmt.Errorf("unexpected multi submission at step %d", b.step)
	}
	b.memberIDs = b.memberIDs[:0]
	for _, item := range items {
		if id, ok := item.Value.(string); ok && id != b.leadID {
			b.memberIDs = append(b.memberIDs, id)
		}
	}
	b.step = stepAskAddGate
	return nil
}

// Cancel rolls back any partially-written filesystem state. Idempotent.
//
// Side effects:
//   - Removes the on-disk manifest when the wizard had advanced past
//     write but not past confirmation, then resets state.
func (b *swarmBuilder) Cancel() {
	b.cancelled = true
	if b.wroteFile && b.swarmsDir != "" && b.name != "" {
		_ = os.Remove(filepath.Join(b.swarmsDir, b.name+".yml"))
		b.wroteFile = false
	}
}

// CompleteMessage returns the final system-message dump rendered after
// the wizard finishes successfully.
//
// Returns:
//   - The completion blurb, or empty when the wizard cancelled.
//
// Side effects:
//   - None.
func (b *swarmBuilder) CompleteMessage() string {
	if b.cancelled {
		return ""
	}
	return b.completionMessage
}

// submitName validates the name input, branching to overwrite
// confirmation when a manifest already exists.
//
// Expected:
//   - value is the trimmed name candidate.
//
// Returns:
//   - nil on success; an error when the name is empty or invalid.
//
// Side effects:
//   - May advance b.step.
func (b *swarmBuilder) submitName(value string) error {
	if value == "" {
		return errors.New("name cannot be empty")
	}
	if strings.ContainsAny(value, " /\\") {
		return errors.New("name must not contain spaces or path separators")
	}
	b.name = value
	if b.swarmsDir != "" {
		path := filepath.Join(b.swarmsDir, value+".yml")
		if _, err := os.Stat(path); err == nil {
			b.step = stepConfirmOverwrite
			return nil
		}
	}
	b.step = stepLead
	return nil
}

// submitOverwriteChoice handles the yes/no overwrite branch.
//
// Returns:
//   - nil on success; an error for an unrecognised choice.
//
// Side effects:
//   - May advance b.step or set b.cancelled.
func (b *swarmBuilder) submitOverwriteChoice(item widgets.Item) error {
	choice, ok := item.Value.(string)
	if !ok {
		return errors.New("invalid choice")
	}
	if choice == confirmNo {
		b.cancelled = true
		b.step = stepFinished
		return nil
	}
	b.overwrite = true
	b.step = stepLead
	return nil
}

// submitLeadChoice records the lead and advances to the members step.
//
// Returns:
//   - nil on success; an error when the value is not a string id.
//
// Side effects:
//   - Advances b.step.
func (b *swarmBuilder) submitLeadChoice(item widgets.Item) error {
	id, ok := item.Value.(string)
	if !ok || id == "" {
		return errors.New("invalid lead selection")
	}
	b.leadID = id
	b.step = stepMembers
	return nil
}

// submitAddGateChoice branches between starting another gate and
// finalising the wizard.
//
// Returns:
//   - nil on success; an error for an unrecognised choice.
//
// Side effects:
//   - Advances b.step.
func (b *swarmBuilder) submitAddGateChoice(item widgets.Item) error {
	choice, ok := item.Value.(string)
	if !ok {
		return errors.New("invalid choice")
	}
	if choice == confirmYes {
		b.step = stepGateName
		return nil
	}
	b.step = stepConfirmWrite
	return nil
}

// submitGateKindChoice records the kind and advances to when.
//
// Returns:
//   - nil on success; an error for an unrecognised choice.
//
// Side effects:
//   - Advances b.step.
func (b *swarmBuilder) submitGateKindChoice(item widgets.Item) error {
	kind, ok := item.Value.(string)
	if !ok || kind == "" {
		return errors.New("invalid gate kind")
	}
	b.pendingGate.Kind = kind
	b.step = stepGateWhen
	return nil
}

// submitGateWhenChoice records when the gate fires.
//
// Returns:
//   - nil on success; an error for an unrecognised choice.
//
// Side effects:
//   - Advances b.step.
func (b *swarmBuilder) submitGateWhenChoice(item widgets.Item) error {
	when, ok := item.Value.(string)
	if !ok || when == "" {
		return errors.New("invalid gate lifecycle")
	}
	b.pendingGate.When = when
	if swarm.IsSwarmLifecyclePoint(when) {
		b.pendingGate.Target = ""
		b.step = stepGateSchema
		return nil
	}
	b.step = stepGateTarget
	return nil
}

// submitGateTargetChoice records the gate's member target. The "none"
// option means swarm-level even when the user has just chosen a
// member-level lifecycle by mistake — defensive but harmless because
// the manifest validator will catch it on save.
//
// Returns:
//   - nil on success; an error for an unrecognised choice.
//
// Side effects:
//   - Advances b.step.
func (b *swarmBuilder) submitGateTargetChoice(item widgets.Item) error {
	target, ok := item.Value.(string)
	if !ok {
		return errors.New("invalid gate target")
	}
	if target == "" {
		b.pendingGate.Target = ""
	} else {
		b.pendingGate.Target = target
	}
	b.step = stepGateSchema
	return nil
}

// submitGateSchemaChoice records the SchemaRef and loops back to the
// "add another gate?" branch.
//
// Returns:
//   - nil on success; an error for an unrecognised choice.
//
// Side effects:
//   - Advances b.step and appends pendingGate to the gate list.
func (b *swarmBuilder) submitGateSchemaChoice(item widgets.Item) error {
	schemaRef, ok := item.Value.(string)
	if !ok {
		return errors.New("invalid schema reference")
	}
	b.pendingGate.SchemaRef = schemaRef
	b.gates = append(b.gates, b.pendingGate)
	b.pendingGate = swarm.GateSpec{}
	b.step = stepAskAddGate
	return nil
}

// submitConfirmWriteChoice handles the final write decision. On "yes"
// it serialises the manifest to disk; on "no" it cancels.
//
// Returns:
//   - nil on success; an error from the YAML write or directory create
//     path otherwise.
//
// Side effects:
//   - Creates / overwrites the manifest file when the user confirms.
func (b *swarmBuilder) submitConfirmWriteChoice(item widgets.Item) error {
	choice, ok := item.Value.(string)
	if !ok {
		return errors.New("invalid choice")
	}
	if choice == confirmNo {
		b.cancelled = true
		b.step = stepFinished
		return nil
	}
	if err := b.writeManifest(); err != nil {
		return err
	}
	b.step = stepFinished
	b.completionMessage = fmt.Sprintf("Wrote swarm manifest to %s", filepath.Join(b.swarmsDir, b.name+".yml"))
	return nil
}

// renderPreview produces the YAML dump shown as a system message before
// the user confirms the write.
//
// Returns:
//   - The YAML rendering of the in-progress manifest.
//
// Side effects:
//   - None.
func (b *swarmBuilder) renderPreview() string {
	manifest := b.buildManifest()
	out, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Sprintf("(failed to render preview: %v)", err)
	}
	return string(out)
}

// buildManifest assembles the swarm.Manifest from the wizard's collected
// state. Pulled out as its own helper so the preview render and the
// final write share a single source of truth.
//
// Returns:
//   - A populated Manifest ready for marshal or validation.
//
// Side effects:
//   - None.
func (b *swarmBuilder) buildManifest() swarm.Manifest {
	chainPrefix := b.chainPrefix
	if chainPrefix == "" {
		chainPrefix = b.name
	}
	return swarm.Manifest{
		SchemaVersion: swarm.SchemaVersionV1,
		ID:            b.name,
		Lead:          b.leadID,
		Members:       append([]string(nil), b.memberIDs...),
		Harness:       swarm.HarnessConfig{Gates: append([]swarm.GateSpec(nil), b.gates...)},
		Context:       swarm.ContextConfig{ChainPrefix: chainPrefix},
	}
}

// writeManifest serialises the in-progress manifest to disk under the
// configured swarms directory. Directory creation uses 0o755 to match
// the rest of the user-config tree.
//
// Returns:
//   - nil on success; the first filesystem error otherwise.
//
// Side effects:
//   - Creates b.swarmsDir if missing and writes the YAML file.
func (b *swarmBuilder) writeManifest() error {
	if b.swarmsDir == "" {
		return errors.New("swarms directory is not configured")
	}
	if err := os.MkdirAll(b.swarmsDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", b.swarmsDir, err)
	}
	out, err := yaml.Marshal(b.buildManifest())
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	path := filepath.Join(b.swarmsDir, b.name+".yml")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	b.wroteFile = true
	return nil
}

// agentItems returns picker items for every registered agent, used at
// the lead-selection step.
//
// Returns:
//   - A slice of items keyed on the agent id.
//
// Side effects:
//   - None.
func (b *swarmBuilder) agentItems() []widgets.Item {
	if b.agents == nil {
		return nil
	}
	manifests := b.agents.List()
	out := make([]widgets.Item, 0, len(manifests))
	for _, m := range manifests {
		out = append(out, widgets.Item{
			Label:       m.ID,
			Description: agentDescription(m),
			Value:       m.ID,
		})
	}
	return out
}

// memberCandidateItems returns picker items for the multi-select
// members step. The lead is filtered out so the user cannot accidentally
// list it as a member (the manifest validator would catch it but
// surfacing the invalid option in the picker is misleading).
//
// Returns:
//   - A slice of items keyed on each candidate agent id.
//
// Side effects:
//   - None.
func (b *swarmBuilder) memberCandidateItems() []widgets.Item {
	if b.agents == nil {
		return nil
	}
	manifests := b.agents.List()
	out := make([]widgets.Item, 0, len(manifests))
	for _, m := range manifests {
		if m.ID == b.leadID {
			continue
		}
		out = append(out, widgets.Item{
			Label:       m.ID,
			Description: agentDescription(m),
			Value:       m.ID,
		})
	}
	return out
}

// gateTargetItems returns picker items for the gate-target step. The
// first item is "none" (swarm-level); subsequent items are the
// confirmed members so a member-level gate cannot be authored against
// an absent member.
//
// Returns:
//   - A slice of items keyed on each member id (or "" for swarm-level).
//
// Side effects:
//   - None.
func (b *swarmBuilder) gateTargetItems() []widgets.Item {
	out := []widgets.Item{
		{Label: "none (swarm-level)", Value: ""},
	}
	for _, id := range b.memberIDs {
		out = append(out, widgets.Item{Label: id, Value: id})
	}
	return out
}

// schemaItems returns picker items for the gate-schema step. When no
// schemas are registered the wizard surfaces a single empty option so
// the user can still finish authoring the gate (the manifest validator
// fires later if SchemaRef is required for the chosen kind).
//
// Returns:
//   - A slice of items keyed on each registered schema name.
//
// Side effects:
//   - None.
func (b *swarmBuilder) schemaItems() []widgets.Item {
	if len(b.schemaNames) == 0 {
		return []widgets.Item{{Label: "(no schemas registered)", Value: ""}}
	}
	out := make([]widgets.Item, 0, len(b.schemaNames))
	for _, name := range b.schemaNames {
		out = append(out, widgets.Item{Label: name, Value: name})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// agentDescription builds the human-facing description string used on
// the lead and members pickers. Pulled out as its own helper so a
// future schema change on agent.Manifest does not mean editing every
// pickerCallSite.
//
// Expected:
//   - m is a non-nil agent.Manifest.
//
// Returns:
//   - "<Name> (<mode>)" where mode is omitted when blank.
//
// Side effects:
//   - None.
func agentDescription(m *agent.Manifest) string {
	if m == nil {
		return ""
	}
	if m.Mode == "" {
		return m.Name
	}
	return fmt.Sprintf("%s (%s)", m.Name, m.Mode)
}

// yesNoItems returns the canonical yes/no item pair used for every
// confirmation step.
//
// Returns:
//   - A two-item slice with stable Value strings.
//
// Side effects:
//   - None.
func yesNoItems() []widgets.Item {
	return []widgets.Item{
		{Label: "yes", Value: confirmYes},
		{Label: "no", Value: confirmNo},
	}
}

// whenItems returns the picker items for the gate-when step.
//
// Returns:
//   - A slice of items keyed on each lifecycle string.
//
// Side effects:
//   - None.
func whenItems() []widgets.Item {
	return []widgets.Item{
		{Label: swarm.LifecyclePreSwarm, Value: swarm.LifecyclePreSwarm},
		{Label: swarm.LifecyclePostSwarm, Value: swarm.LifecyclePostSwarm},
		{Label: swarm.LifecyclePreMember, Value: swarm.LifecyclePreMember},
		{Label: swarm.LifecyclePostMember, Value: swarm.LifecyclePostMember},
	}
}
