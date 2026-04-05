package support

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
)

// RegisterAgentLayeringSteps registers step definitions for layered agent discovery tests.
//
// Expected:
//   - ctx is a non-nil godog ScenarioContext for step registration.
//
// Side effects:
//   - Registers agent layering step patterns on the scenario context.
func (s *StepDefinitions) RegisterAgentLayeringSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^an agent registry already contains an agent with ID "([^"]*)"$`,
		s.anAgentRegistryAlreadyContainsAnAgentWithID)
	ctx.Step(`^I call DiscoverMerge on a directory containing an agent with ID "([^"]*)"$`,
		s.iCallDiscoverMergeOnADirectoryContainingAnAgentWithID)
	ctx.Step(`^the registry should contain both "([^"]*)" and "([^"]*)"$`,
		s.theRegistryShouldContainBothAnd)

	ctx.Step(`^an agent registry already contains a bundled agent with ID "([^"]*)"$`,
		s.anAgentRegistryAlreadyContainsABundledAgentWithID)
	ctx.Step(`^I call DiscoverMerge on a directory containing a user override for "([^"]*)"$`,
		s.iCallDiscoverMergeOnADirectoryContainingAUserOverrideFor)
	ctx.Step(`^the registry should contain the user "([^"]*)" agent$`,
		s.theRegistryShouldContainTheUserAgent)
	ctx.Step(`^the bundled "([^"]*)" should have been replaced$`,
		s.theBundledShouldHaveBeenReplaced)

	ctx.Step(`^a primary agent directory containing a "([^"]*)" agent$`,
		s.aPrimaryAgentDirectoryContainingAgent)
	ctx.Step(`^an AgentDirs entry containing a "([^"]*)" override and a "([^"]*)"$`,
		s.anExtraAgentDirectoryContainingAnOverrideAnd)
	ctx.Step(`^the app sets up the agent registry with layered discovery$`,
		s.theAppSetsUpTheAgentRegistryWithLayeredDiscovery)
	ctx.Step(`^the registry should contain "([^"]*)" from AgentDirs$`,
		s.theRegistryShouldContainFromTheExtraDirectory)
	ctx.Step(`^the registry should contain the "([^"]*)" from AgentDirs overriding the primary$`,
		s.theRegistryShouldContainFromTheExtraDirectoryOverridingPrimary)
}

// anAgentRegistryAlreadyContainsAnAgentWithID pre-populates a registry with the given agent ID.
//
// Expected:
//   - id is a non-empty agent identifier.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Creates s.agentRegistry and registers a manifest with the given ID.
func (s *StepDefinitions) anAgentRegistryAlreadyContainsAnAgentWithID(id string) error {
	s.agentRegistry = agent.NewRegistry()
	s.agentRegistry.Register(&agent.Manifest{
		ID:   id,
		Name: id + " Agent",
	})
	return nil
}

// iCallDiscoverMergeOnADirectoryContainingAnAgentWithID writes a manifest for the given ID
// to a temp directory and calls DiscoverMerge on the registry.
//
// Expected:
//   - s.agentRegistry has been initialised.
//   - id is a non-empty agent identifier.
//
// Returns:
//   - nil on success, or an error if file creation or discovery fails.
//
// Side effects:
//   - Creates a temp directory with a JSON manifest and calls DiscoverMerge.
func (s *StepDefinitions) iCallDiscoverMergeOnADirectoryContainingAnAgentWithID(id string) error {
	if s.agentRegistry == nil {
		return errors.New("agent registry not initialised")
	}
	dir, err := os.MkdirTemp("", "flowstate-merge-")
	if err != nil {
		return fmt.Errorf("creating merge temp dir: %w", err)
	}
	content := fmt.Sprintf(`{"schema_version": "1", "id": %q, "name": %q}`, id, id+" Agent")
	manifestPath := filepath.Join(dir, id+".json")
	if err := os.WriteFile(manifestPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing merge manifest: %w", err)
	}
	return s.agentRegistry.DiscoverMerge(dir)
}

// theRegistryShouldContainBothAnd verifies both named agents exist in the registry.
//
// Expected:
//   - s.agentRegistry has been populated.
//
// Returns:
//   - nil if both agents are present, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theRegistryShouldContainBothAnd(first, second string) error {
	if s.agentRegistry == nil {
		return errors.New("agent registry not initialised")
	}
	if _, ok := s.agentRegistry.Get(first); !ok {
		return fmt.Errorf("expected registry to contain %q", first)
	}
	if _, ok := s.agentRegistry.Get(second); !ok {
		return fmt.Errorf("expected registry to contain %q", second)
	}
	return nil
}

// anAgentRegistryAlreadyContainsABundledAgentWithID pre-populates a registry with a
// bundled-flavoured agent so a later override can be asserted.
//
// Expected:
//   - id is a non-empty agent identifier.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Creates s.agentRegistry and registers a manifest whose system prompt marks it as bundled.
func (s *StepDefinitions) anAgentRegistryAlreadyContainsABundledAgentWithID(id string) error {
	s.agentRegistry = agent.NewRegistry()
	s.agentRegistry.Register(&agent.Manifest{
		ID:   id,
		Name: "Bundled " + id,
		Instructions: agent.Instructions{
			SystemPrompt: "bundled source for " + id,
		},
	})
	return nil
}

// iCallDiscoverMergeOnADirectoryContainingAUserOverrideFor writes a user-flavoured
// manifest to a temp directory and calls DiscoverMerge, overriding an existing entry.
//
// Expected:
//   - s.agentRegistry has been initialised.
//   - id is a non-empty agent identifier present in the registry.
//
// Returns:
//   - nil on success, or an error if file creation or discovery fails.
//
// Side effects:
//   - Creates a temp directory with a markdown manifest and calls DiscoverMerge.
func (s *StepDefinitions) iCallDiscoverMergeOnADirectoryContainingAUserOverrideFor(id string) error {
	if s.agentRegistry == nil {
		return errors.New("agent registry not initialised")
	}
	dir, err := os.MkdirTemp("", "flowstate-override-")
	if err != nil {
		return fmt.Errorf("creating override temp dir: %w", err)
	}
	content := fmt.Sprintf("---\nid: %s\nname: User %s\nschema_version: \"1\"\n---\nuser source for %s\n", id, id, id)
	manifestPath := filepath.Join(dir, id+".md")
	if err := os.WriteFile(manifestPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing override manifest: %w", err)
	}
	return s.agentRegistry.DiscoverMerge(dir)
}

// theRegistryShouldContainTheUserAgent verifies the registered agent is the user override.
//
// Expected:
//   - s.agentRegistry has been populated via DiscoverMerge.
//
// Returns:
//   - nil if the agent's body or name marks it as user-sourced, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theRegistryShouldContainTheUserAgent(id string) error {
	if s.agentRegistry == nil {
		return errors.New("agent registry not initialised")
	}
	manifest, ok := s.agentRegistry.Get(id)
	if !ok {
		return fmt.Errorf("expected registry to contain %q", id)
	}
	if !strings.Contains(manifest.Name, "User") && !strings.Contains(manifest.Instructions.SystemPrompt, "user source") {
		return fmt.Errorf("expected user-sourced %q, got name=%q prompt=%q",
			id, manifest.Name, manifest.Instructions.SystemPrompt)
	}
	return nil
}

// theBundledShouldHaveBeenReplaced verifies that no bundled marker remains for the agent.
//
// Expected:
//   - s.agentRegistry has been populated via DiscoverMerge.
//
// Returns:
//   - nil if the manifest no longer contains bundled markers, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theBundledShouldHaveBeenReplaced(id string) error {
	if s.agentRegistry == nil {
		return errors.New("agent registry not initialised")
	}
	manifest, ok := s.agentRegistry.Get(id)
	if !ok {
		return fmt.Errorf("expected registry to contain %q", id)
	}
	if strings.Contains(manifest.Name, "Bundled") || strings.Contains(manifest.Instructions.SystemPrompt, "bundled source") {
		return fmt.Errorf("expected bundled %q to be replaced, still present: name=%q prompt=%q",
			id, manifest.Name, manifest.Instructions.SystemPrompt)
	}
	return nil
}

// aPrimaryAgentDirectoryContainingAgent creates a primary temp directory with a manifest
// marked as coming from the primary (bundled) source.
//
// Expected:
//   - id is a non-empty agent identifier.
//
// Returns:
//   - nil on success, or an error if file creation fails.
//
// Side effects:
//   - Creates s.primaryAgentDir and writes a manifest to it.
func (s *StepDefinitions) aPrimaryAgentDirectoryContainingAgent(id string) error {
	dir, err := os.MkdirTemp("", "flowstate-primary-agent-")
	if err != nil {
		return fmt.Errorf("creating primary agent dir: %w", err)
	}
	content := fmt.Sprintf("---\nid: %s\nname: Primary %s\nschema_version: \"1\"\n---\nprimary source for %s\n", id, id, id)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing primary manifest: %w", err)
	}
	s.primaryAgentDir = dir
	return nil
}

// anExtraAgentDirectoryContainingAnOverrideAnd creates an extra temp directory with two
// manifests: an override for an existing agent and a new custom agent.
//
// Expected:
//   - overrideID and customID are non-empty agent identifiers.
//
// Returns:
//   - nil on success, or an error if file creation fails.
//
// Side effects:
//   - Creates s.extraAgentDir and writes two manifests to it.
func (s *StepDefinitions) anExtraAgentDirectoryContainingAnOverrideAnd(overrideID, customID string) error {
	dir, err := os.MkdirTemp("", "flowstate-extra-agent-")
	if err != nil {
		return fmt.Errorf("creating extra agent dir: %w", err)
	}
	overrideContent := fmt.Sprintf(
		"---\nid: %s\nname: Extra %s\nschema_version: \"1\"\n---\nextra source for %s\n",
		overrideID, overrideID, overrideID,
	)
	if err := os.WriteFile(filepath.Join(dir, overrideID+".md"), []byte(overrideContent), 0o600); err != nil {
		return fmt.Errorf("writing extra override manifest: %w", err)
	}
	customContent := fmt.Sprintf(
		"---\nid: %s\nname: Custom %s\nschema_version: \"1\"\n---\ncustom agent in extra dir\n",
		customID, customID,
	)
	if err := os.WriteFile(filepath.Join(dir, customID+".md"), []byte(customContent), 0o600); err != nil {
		return fmt.Errorf("writing extra custom manifest: %w", err)
	}
	s.extraAgentDir = dir
	return nil
}

// theAppSetsUpTheAgentRegistryWithLayeredDiscovery invokes the layered discovery entry
// point used by the application to populate the registry from the primary and extra dirs.
//
// Expected:
//   - s.primaryAgentDir and s.extraAgentDir are populated from prior steps.
//
// Returns:
//   - nil on success, or an error if layered discovery fails.
//
// Side effects:
//   - Populates s.agentRegistry by invoking the exported layered setup helper.
func (s *StepDefinitions) theAppSetsUpTheAgentRegistryWithLayeredDiscovery() error {
	cfg := &config.AppConfig{
		AgentDir:  s.primaryAgentDir,
		AgentDirs: []string{s.extraAgentDir},
	}
	s.agentRegistry = app.SetupAgentRegistryForTest(cfg)
	return nil
}

// theRegistryShouldContainFromTheExtraDirectory verifies an agent exists and originates
// from the extra directory (identified by its name prefix or system prompt marker).
//
// Expected:
//   - s.agentRegistry has been populated via layered discovery.
//
// Returns:
//   - nil if the agent is present and sourced from the extra directory, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theRegistryShouldContainFromTheExtraDirectory(id string) error {
	if s.agentRegistry == nil {
		return errors.New("agent registry not initialised")
	}
	manifest, ok := s.agentRegistry.Get(id)
	if !ok {
		return fmt.Errorf("expected registry to contain %q", id)
	}
	if !strings.Contains(manifest.Name, "Extra") && !strings.Contains(manifest.Name, "Custom") {
		return fmt.Errorf("expected %q from extra directory, got name=%q", id, manifest.Name)
	}
	return nil
}

// theRegistryShouldContainFromTheExtraDirectoryOverridingPrimary verifies the named
// agent in the registry came from the extra directory, not the primary.
//
// Expected:
//   - s.agentRegistry has been populated via layered discovery.
//
// Returns:
//   - nil if the agent's markers indicate the extra-directory source, error otherwise.
//
// Side effects:
//   - None.
func (s *StepDefinitions) theRegistryShouldContainFromTheExtraDirectoryOverridingPrimary(id string) error {
	if s.agentRegistry == nil {
		return errors.New("agent registry not initialised")
	}
	manifest, ok := s.agentRegistry.Get(id)
	if !ok {
		return fmt.Errorf("expected registry to contain %q", id)
	}
	if strings.Contains(manifest.Name, "Primary") {
		return fmt.Errorf("expected %q to be overridden by extra dir, still primary: %q", id, manifest.Name)
	}
	if !strings.Contains(manifest.Name, "Extra") {
		return fmt.Errorf("expected %q from extra directory, got name=%q", id, manifest.Name)
	}
	return nil
}
