//go:build e2e

// Package support provides BDD test step definitions and helpers.
package support

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
)

// skillsGuaranteeSteps holds per-scenario state for the skills-guarantee
// invariant feature: one scenario streams a real turn against an engine
// carrying an always-active skill, the other drives the loader with a
// manifest naming a skill that does not exist on disk.
type skillsGuaranteeSteps struct {
	sessionID       string
	skillName       string
	skillBody       string
	provider        *invariantScriptedProvider
	store           *recall.FileContextStore
	storeDir        string
	skillsDir       string
	loaderCapturer  *invariantLogCapturer
	loaderSkills    []skill.Skill
	loaderErr       error
	loaderRequested []string
}

// RegisterSkillsGuaranteeSteps wires the skills-guarantee invariant
// feature steps onto the godog scenario context.
func RegisterSkillsGuaranteeSteps(ctx *godog.ScenarioContext) {
	s := &skillsGuaranteeSteps{}
	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset("skills-guarantee-session")
		return bddCtx, nil
	})
	ctx.AfterScenario(func(_ *godog.Scenario, _ error) {
		s.teardown()
	})
	ctx.Step(`^an engine carries one always-active skill$`, s.engineCarriesAlwaysActiveSkill)
	ctx.Step(`^the always-active turn is streamed once$`, s.alwaysActiveTurnStreamedOnce)
	ctx.Step(`^the first provider request embeds the always-active skill body$`, s.firstRequestEmbedsSkillBody)
	ctx.Step(`^a manifest lists an always-active skill that is missing on disk$`, s.manifestListsSkillMissingOnDisk)
	ctx.Step(`^the missing skill is reported by the loader or validator$`, s.missingSkillReported)
}

// reset rebuilds per-scenario state.
func (s *skillsGuaranteeSteps) reset(sessionID string) {
	s.sessionID = sessionID
	s.skillName = "invariant-gatekeeper"
	s.skillBody = "INVARIANT_ALWAYS_ACTIVE_BODY: classify before acting."
	s.provider = &invariantScriptedProvider{name: "skills-guarantee-provider"}
	s.provider.turns = []invariantTurn{{content: "All done."}}
	dir, err := os.MkdirTemp("", "skills-guarantee-ctx-*")
	if err != nil {
		return
	}
	store, err := recall.NewFileContextStore(dir+"/ctx.json", "invariant-model")
	if err != nil {
		return
	}
	s.storeDir = dir
	s.store = store
	s.skillsDir = ""
	s.loaderCapturer = nil
	s.loaderSkills = nil
	s.loaderErr = nil
	s.loaderRequested = nil
}

// teardown closes the scenario's context store and removes its temp dir.
func (s *skillsGuaranteeSteps) teardown() {
	if s.store != nil {
		s.store.Close()
		s.store = nil
	}
	if s.storeDir != "" {
		os.RemoveAll(s.storeDir)
		s.storeDir = ""
	}
	if s.skillsDir != "" {
		os.RemoveAll(s.skillsDir)
		s.skillsDir = ""
	}
}

// engineCarriesAlwaysActiveSkill is satisfied by the reset state, which
// wires the skill body the engine will carry.
func (s *skillsGuaranteeSteps) engineCarriesAlwaysActiveSkill() error {
	if s.store == nil {
		return fmt.Errorf("scenario store was not initialised")
	}
	return nil
}

// alwaysActiveTurnStreamedOnce streams a single text-only turn through
// an engine constructed with one always-active skill.
func (s *skillsGuaranteeSteps) alwaysActiveTurnStreamedOnce() error {
	manifest := agent.Manifest{
		ID:   "skills-guarantee-agent",
		Name: "Skills Guarantee Agent",
	}
	eng := engine.New(engine.Config{
		ChatProvider: s.provider,
		Manifest:     manifest,
		Store:        s.store,
		Skills: []skill.Skill{
			{Name: s.skillName, Content: s.skillBody},
		},
		ToolOutputRetention: -1,
	})
	streamCtx, cancel := context.WithCancel(context.WithValue(context.Background(), session.IDKey{}, s.sessionID))
	defer cancel()
	chunks, err := eng.Stream(streamCtx, s.sessionID, "Go")
	if err != nil {
		return err
	}
	for range chunks {
	}
	return nil
}

// firstRequestEmbedsSkillBody asserts the first provider request carried
// the always-active skill body in a system message.
func (s *skillsGuaranteeSteps) firstRequestEmbedsSkillBody() error {
	requests := s.provider.recordedRequests()
	if len(requests) == 0 {
		return fmt.Errorf("the provider never received a request")
	}
	for _, m := range requests[0].Messages {
		if m.Role != "system" {
			continue
		}
		if strings.Contains(m.Content, s.skillBody) {
			return nil
		}
	}
	return fmt.Errorf("the first provider request carried no system message embedding %q", s.skillBody)
}

// manifestListsSkillMissingOnDisk prepares a skills directory holding
// one real skill and asks the loader for it plus a skill that does not
// exist on disk, under a captured logger.
func (s *skillsGuaranteeSteps) manifestListsSkillMissingOnDisk() error {
	dir, err := os.MkdirTemp("", "skills-guarantee-skills-*")
	if err != nil {
		return err
	}
	s.skillsDir = dir
	presentDir := filepath.Join(dir, "present-skill")
	if err := os.MkdirAll(presentDir, 0o755); err != nil {
		return err
	}
	body := "---\nname: present-skill\ndescription: A skill that exists on disk\n---\nBody of the present skill.\n"
	if err := os.WriteFile(filepath.Join(presentDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		return err
	}

	s.loaderRequested = []string{"present-skill", "ghost-skill"}
	s.loaderCapturer = &invariantLogCapturer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(s.loaderCapturer))
	s.loaderSkills = engine.LoadAlwaysActiveSkills(dir, s.loaderRequested, nil)
	slog.SetDefault(previous)
	return nil
}

func (s *skillsGuaranteeSteps) missingSkillReported() error {
	if s.loaderCapturer == nil {
		return fmt.Errorf("the loader was never invoked")
	}
	for message, count := range s.loaderCapturer.messageSnapshot() {
		if count > 0 && strings.Contains(message, "ghost-skill") {
			return nil
		}
	}
	if len(s.loaderSkills) == len(s.loaderRequested) {
		return nil
	}
	return fmt.Errorf("missing always-active skill %q was silently dropped: loader returned %d of %d requested skills, logged %d distinct messages naming nothing about it, and the API has no error return",
		"ghost-skill", len(s.loaderSkills), len(s.loaderRequested), len(s.loaderCapturer.messageSnapshot()))
}
