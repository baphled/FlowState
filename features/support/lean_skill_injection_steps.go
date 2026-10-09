//go:build e2e

package support

import (
	"context"
	"fmt"
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

// leanSkillInjectionSteps holds per-scenario state for the lean skill
// injection feature. The contract under test: no prompt build embeds a
// full skill body — only a lean name header that points the model at
// skill_load for in-session loading.
type leanSkillInjectionSteps struct {
	skills         []skill.Skill
	skillsResolver func(agent.Manifest) []skill.Skill
	prompt         string
	rebuiltPrompt  string
	resolverCalls  int
	sessionID      string
	skillBody      string
	provider       *invariantScriptedProvider
	eng            *engine.Engine
	store          *recall.FileContextStore
	storeDir       string
}

// RegisterLeanSkillInjectionSteps wires the lean skill injection steps
// onto the godog scenario context.
//
// Expected:
//   - ctx is a godog ScenarioContext.
//
// Side effects:
//   - Registers lean skill injection step definitions.
func RegisterLeanSkillInjectionSteps(ctx *godog.ScenarioContext) {
	s := &leanSkillInjectionSteps{}
	ctx.Step(`^an engine configured with always-active skills "([^"]*)" and "([^"]*)"$`, s.engineWithAlwaysActiveSkills)
	ctx.Step(`^an engine configured with always-active skills "([^"]*)" and "([^"]*)" and a skills resolver for a second manifest with skill "([^"]*)"$`, s.engineWithResolver)
	ctx.Step(`^the system prompt is built for skills$`, s.theSystemPromptIsBuilt)
	ctx.Step(`^the system prompt should contain the lean name header for skill "([^"]*)"$`, s.promptContainsLeanHeader)
	ctx.Step(`^the system prompt should not contain the full body of any skill$`, s.promptContainsNoBodies)
	ctx.Step(`^an engine carries one always-active skill$`, s.engineCarriesAlwaysActiveSkill)
	ctx.Step(`^the always-active turn is streamed once$`, s.alwaysActiveTurnStreamedOnce)
	ctx.Step(`^the first provider request names the always-active skill without its body$`, s.firstRequestNamesSkillWithoutBody)
	ctx.Step(`^the engine's manifest is swapped and the system prompt is rebuilt$`, s.manifestSwappedAndRebuilt)
	ctx.Step(`^the rebuilt system prompt contains the lean name header for skill "([^"]*)"$`, s.rebuiltPromptContainsLeanHeader)
	ctx.Step(`^the rebuilt system prompt does not contain the full body of any skill$`, s.rebuiltPromptContainsNoBodies)
}

func (s *leanSkillInjectionSteps) engineWithAlwaysActiveSkills(first, second string) error {
	s.skills = []skill.Skill{
		{Name: first, Content: "PREFLIGHT_BODY"},
		{Name: second, Content: "MEMORY_KEEPER_BODY"},
	}
	return nil
}

func (s *leanSkillInjectionSteps) engineWithResolver(first, second, swapped string) error {
	if err := s.engineWithAlwaysActiveSkills(first, second); err != nil {
		return err
	}
	s.resolverCalls = 0
	s.skillsResolver = func(_ agent.Manifest) []skill.Skill {
		s.resolverCalls++
		return []skill.Skill{{Name: swapped, Content: "MEMORY_KEEPER_BODY"}}
	}
	// Build the engine eagerly so the manifest-swap scenario has s.eng
	// even though it never runs the "system prompt is built" step.
	cfg := engine.Config{Skills: s.skills, SkillsResolver: s.skillsResolver}
	eng := engine.New(cfg)
	s.eng = eng
	s.prompt = eng.BuildSystemPrompt()
	if s.prompt == "" {
		return godog.ErrUndefined
	}
	return nil
}

func (s *leanSkillInjectionSteps) theSystemPromptIsBuilt() error {
	cfg := engine.Config{Skills: s.skills}
	if s.skillsResolver != nil {
		cfg.SkillsResolver = s.skillsResolver
	}
	eng := engine.New(cfg)
	s.eng = eng
	s.prompt = eng.BuildSystemPrompt()
	if s.prompt == "" {
		return godog.ErrUndefined
	}
	return nil
}

func (s *leanSkillInjectionSteps) promptContainsLeanHeader(name string) error {
	if !strings.Contains(s.prompt, name) {
		return fmt.Errorf("prompt does not name skill %q", name)
	}
	return nil
}

func (s *leanSkillInjectionSteps) promptContainsNoBodies() error {
	return assertNoSkillBodies(s.prompt)
}

func (s *leanSkillInjectionSteps) engineCarriesAlwaysActiveSkill() error {
	s.sessionID = "lean-skill-injection-session"
	s.skillBody = "INVARIANT_ALWAYS_ACTIVE_BODY: classify before acting."
	s.provider = &invariantScriptedProvider{name: "lean-skill-provider"}
	s.provider.turns = []invariantTurn{{content: "All done."}}
	return nil
}

func (s *leanSkillInjectionSteps) alwaysActiveTurnStreamedOnce() error {
	if s.store == nil {
		dir, err := os.MkdirTemp("", "lean-skill-injection-")
		if err != nil {
			return err
		}
		s.storeDir = dir
		store, err := recall.NewFileContextStore(filepath.Join(dir, "ctx.json"), "lean-skill-model")
		if err != nil {
			return err
		}
		s.store = store
	}
	manifest := agent.Manifest{ID: "lean-skill-agent", Name: "Lean Skill Agent"}
	eng := engine.New(engine.Config{
		ChatProvider: s.provider,
		Manifest:     manifest,
		Store:        s.store,
		Skills: []skill.Skill{
			{Name: "invariant-gatekeeper", Content: s.skillBody},
		},
		ToolOutputRetention: -1,
	})
	s.eng = eng
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

func (s *leanSkillInjectionSteps) firstRequestNamesSkillWithoutBody() error {
	requests := s.provider.recordedRequests()
	if len(requests) == 0 {
		return fmt.Errorf("the provider never received a request")
	}
	for _, m := range requests[0].Messages {
		if m.Role != "system" {
			continue
		}
		if strings.Contains(m.Content, s.skillBody) {
			return fmt.Errorf("system message embeds the full skill body")
		}
		if strings.Contains(m.Content, "invariant-gatekeeper") {
			return nil
		}
	}
	return fmt.Errorf("no system message names the always-active skill")
}

func (s *leanSkillInjectionSteps) manifestSwappedAndRebuilt() error {
	if s.eng == nil {
		return fmt.Errorf("no engine was built")
	}
	s.eng.SetManifest(agent.Manifest{ID: "lean-second-agent", Name: "Second Agent"})
	s.rebuiltPrompt = s.eng.BuildSystemPrompt()
	if s.rebuiltPrompt == "" {
		return godog.ErrUndefined
	}
	return nil
}

func (s *leanSkillInjectionSteps) rebuiltPromptContainsLeanHeader(name string) error {
	if !strings.Contains(s.rebuiltPrompt, name) {
		return fmt.Errorf("rebuilt prompt does not name skill %q", name)
	}
	return nil
}

func (s *leanSkillInjectionSteps) rebuiltPromptContainsNoBodies() error {
	return assertNoSkillBodies(s.rebuiltPrompt)
}

// assertNoSkillBodies fails when the prompt embeds any marker body
// used by these scenarios, or the canonical "# Skill: " full-body
// header emitted by the legacy baking path.
func assertNoSkillBodies(prompt string) error {
	if strings.Contains(prompt, "# Skill: ") {
		return fmt.Errorf("prompt embeds a legacy full-body skill block")
	}
	for _, body := range []string{"PREFLIGHT_BODY", "MEMORY_KEEPER_BODY"} {
		if strings.Contains(prompt, body) {
			return fmt.Errorf("prompt embeds skill body %q", body)
		}
	}
	return nil
}
