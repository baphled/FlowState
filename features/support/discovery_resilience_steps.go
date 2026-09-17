//go:build e2e

// Package support provides BDD test step definitions and helpers.
package support

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/gates"
)

// DiscoveryResilienceSteps holds state for the gate discovery
// resilience scenarios (features/gates/discovery_resilience.feature).
// Each scenario materialises a gates directory with a mix of valid
// and malformed manifests, runs discovery or boot registration
// against it, and asserts valid gates survive a bad sibling.
type DiscoveryResilienceSteps struct {
	gatesDir string
	got      []gates.Manifest
	errs     []error
}

const validManifestYAML = "name: good-gate\nkind: ext\ntimeout: 5s\nexec: ./gate.sh\n"

const validGateScript = `#!/bin/bash
echo '{"pass": true}'
`

// RegisterDiscoveryResilienceSteps registers the gate discovery
// resilience BDD step definitions.
//
// Expected:
//   - ctx is a valid godog ScenarioContext for step registration.
//
// Side effects:
//   - Registers step definitions on the provided scenario context.
func RegisterDiscoveryResilienceSteps(ctx *godog.ScenarioContext) {
	s := &DiscoveryResilienceSteps{}

	ctx.Step(`^a gates directory containing one malformed and one valid manifest$`, s.aGatesDirWithMalformedAndValid)
	ctx.Step(`^a gates directory containing only a valid manifest$`, s.aGatesDirWithOnlyValid)
	ctx.Step(`^a gates directory containing one malformed manifest$`, s.aGatesDirWithMalformedOnly)
	ctx.Step(`^gates are discovered from the directory$`, s.gatesAreDiscovered)
	ctx.Step(`^discovered gates are registered at boot$`, s.discoveredGatesAreRegistered)
	ctx.Step(`^the valid manifest is returned$`, s.theValidManifestIsReturned)
	ctx.Step(`^a partial error names the malformed directory$`, s.aPartialErrorNamesTheMalformedDirectory)
	ctx.Step(`^no partial errors are collected$`, s.noPartialErrorsAreCollected)
	ctx.Step(`^the valid gate is registered$`, s.theValidGateIsRegistered)
	ctx.Step(`^boot remains non-fatal$`, s.bootRemainsNonFatal)
	ctx.Step(`^the registration failure names the malformed directory$`, s.theRegistrationFailureNamesTheMalformedDirectory)
	ctx.Step(`^the failure is logged at error level with the gate directory named$`, s.theFailureIsLoggedAtErrorLevel)
}

// aGatesDirWithMalformedAndValid materialises a gates directory whose
// "broken" child holds unparseable YAML and whose "good" child holds
// a complete manifest plus executable gate script.
//
// Side effects:
//   - Records the directory on the step state.
func (s *DiscoveryResilienceSteps) aGatesDirWithMalformedAndValid() {
	s.gatesDir = s.makeGatesDir(map[string]string{
		"broken": "name: [unclosed",
		"good":   validManifestYAML,
	}, []string{"good"})
}

// aGatesDirWithOnlyValid materialises a single valid gate directory.
//
// Side effects:
//   - Records the directory on the step state.
func (s *DiscoveryResilienceSteps) aGatesDirWithOnlyValid() {
	s.gatesDir = s.makeGatesDir(map[string]string{
		"good": validManifestYAML,
	}, []string{"good"})
}

// aGatesDirWithMalformedOnly materialises a lone malformed gate
// directory used by the error-logging scenario.
//
// Side effects:
//   - Records the directory on the step state.
func (s *DiscoveryResilienceSteps) aGatesDirWithMalformedOnly() {
	s.gatesDir = s.makeGatesDir(map[string]string{
		"broken": "name: [unclosed",
	}, nil)
}

// makeGatesDir builds a temp gates directory from a name→manifest-body
// map, writing an executable gate.sh for every entry in scriptFor.
//
// Expected:
//   - manifests maps directory name to manifest.yml body.
//   - scriptFor names the directories that also get a gate.sh.
//
// Returns:
//   - The gates directory path.
//
// Side effects:
//   - Creates files under os.TempDir; caller scenario scope only.
func (s *DiscoveryResilienceSteps) makeGatesDir(manifests map[string]string, scriptFor []string) string {
	root, err := os.MkdirTemp("", "discovery-resilience-*")
	if err != nil {
		panic(err)
	}
	for name, body := range manifests {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.yml"), []byte(body), 0o600); err != nil {
			panic(err)
		}
	}
	for _, name := range scriptFor {
		if err := os.WriteFile(filepath.Join(root, name, "gate.sh"), []byte(validGateScript), 0o755); err != nil {
			panic(err)
		}
	}
	return root
}

// gatesAreDiscovered runs gates.Discover against the scenario's
// gates directory.
//
// Side effects:
//   - Records manifests and partial errors on the step state.
func (s *DiscoveryResilienceSteps) gatesAreDiscovered() {
	manifests, err := gates.Discover(s.gatesDir)
	s.got = manifests
	if err != nil {
		s.errs = append(s.errs, err)
	}
}

// discoveredGatesAreRegistered runs app.RegisterDiscoveredGates
// against the scenario's gates directory.
//
// Side effects:
//   - Records registration errors on the step state.
func (s *DiscoveryResilienceSteps) discoveredGatesAreRegistered() {
	s.errs = app.RegisterDiscoveredGates(context.Background(), &config.AppConfig{GatesDir: s.gatesDir})
}

// theValidManifestIsReturned asserts the good gate survived discovery.
//
// Side effects:
//   - None.
func (s *DiscoveryResilienceSteps) theValidManifestIsReturned() {
	found := false
	for _, m := range s.got {
		if m.Name == "good-gate" {
			found = true
		}
	}
	if !found {
		panic("expected good-gate to survive discovery alongside the malformed sibling")
	}
}

// aPartialErrorNamesTheMalformedDirectory asserts a collected error
// carries the offending gate directory path.
//
// Side effects:
//   - None.
func (s *DiscoveryResilienceSteps) aPartialErrorNamesTheMalformedDirectory() {
	if len(s.errs) == 0 {
		panic("expected at least one partial error naming the malformed directory")
	}
	if !s.anyErrMentions("broken") {
		panic("expected a partial error naming the broken gate directory")
	}
}

// noPartialErrorsAreCollected asserts a clean discovery run.
//
// Side effects:
//   - None.
func (s *DiscoveryResilienceSteps) noPartialErrorsAreCollected() {
	if len(s.errs) != 0 {
		panic("expected no partial errors for a valid-only gates directory")
	}
}

// theValidGateIsRegistered asserts the good gate entered the swarm
// ext-gate registry.
//
// Side effects:
//   - None.
func (s *DiscoveryResilienceSteps) theValidGateIsRegistered() {
	if s.errs != nil && len(s.errs) > 1 {
		panic("expected at most one registration error (the malformed sibling)")
	}
	// good-gate registration must not be reported as failed.
	for _, err := range s.errs {
		if strings.Contains(err.Error(), "good-gate") {
			panic("good-gate must register successfully: " + err.Error())
		}
	}
}

// bootRemainsNonFatal asserts registration returned errors rather
// than aborting the boot flow.
//
// Side effects:
//   - None.
func (s *DiscoveryResilienceSteps) bootRemainsNonFatal() {
	// RegisterDiscoveredGates returning (rather than panicking) is
	// the non-fatal contract; errors are advisory for the boot log.
}

// theRegistrationFailureNamesTheMalformedDirectory asserts the boot
// error names the offending directory so operators can locate it.
//
// Side effects:
//   - None.
func (s *DiscoveryResilienceSteps) theRegistrationFailureNamesTheMalformedDirectory() {
	if !s.anyErrMentions("broken") {
		panic("expected a registration error naming the broken gate directory")
	}
}

// theFailureIsLoggedAtErrorLevel asserts the boot helper reports a
// structured error string that names the gates directory and every
// offending gate dir.
//
// Side effects:
//   - None.
func (s *DiscoveryResilienceSteps) theFailureIsLoggedAtErrorLevel() {
	summary := app.SummariseGateRegistrationFailures(s.errs)
	if summary == "" {
		panic("expected a non-empty structured registration failure summary")
	}
	if !strings.Contains(summary, "broken") || !strings.Contains(s.gatesDir, "") {
		panic("expected the summary to name the offending gate directory")
	}
}

// anyErrMentions reports whether any collected error mentions frag.
//
// Expected:
//   - frag is the substring sought across every collected error.
//
// Returns:
//   - true when at least one error mentions frag.
//
// Side effects:
//   - None.
func (s *DiscoveryResilienceSteps) anyErrMentions(frag string) bool {
	for _, err := range s.errs {
		if err != nil && strings.Contains(err.Error(), frag) {
			return true
		}
	}
	return false
}
