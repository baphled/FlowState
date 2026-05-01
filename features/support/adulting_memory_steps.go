//go:build e2e

package support

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"gopkg.in/yaml.v3"
)

type adultingSwarmState struct {
	manifests   map[string]map[string]interface{}
	swarmDir    string
	currentID   string
	currentData map[string]interface{}
}

var adultingState *adultingSwarmState

func init() {
	adultingState = &adultingSwarmState{
		manifests: make(map[string]map[string]interface{}),
	}
}

func RegisterAdultingMemorySteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the adulting swarm is defined$`, adultingState.theAdultingSwarmIsDefined)
	ctx.Step(`^the adulting agent manifests are loaded$`, adultingState.theAdultingAgentManifestsAreLoaded)
	ctx.Step(`^the "([^"]*)" agent manifest$`, adultingState.theAgentManifest)
	ctx.Step(`^it should have "([^"]*)" set to "([^"]*)"$`, adultingState.itShouldHaveSetTo)
	ctx.Step(`^its capabilities\.tools should include "([^"]*)"$`, adultingState.itsCapabilitiesToolsShouldInclude)
	ctx.Step(`^its capabilities\.tools should not include "([^"]*)"$`, adultingState.itsCapabilitiesToolsShouldNotInclude)
	ctx.Step(`^its always_active_skills should include "([^"]*)"$`, adultingState.itsAlwaysActiveSkillsShouldInclude)
	ctx.Step(`^its always_active_skills should not include "([^"]*)"$`, adultingState.itsAlwaysActiveSkillsShouldNotInclude)
}

func (s *adultingSwarmState) reset() {
	s.manifests = make(map[string]map[string]interface{})
	s.currentID = ""
	s.currentData = nil
	s.swarmDir = ""
}

func (s *adultingSwarmState) theAdultingSwarmIsDefined() error {
	s.reset()

	repoRoot := findRepoRoot()
	swarmYML := filepath.Join(repoRoot, "examples", "swarms", "adulting", "adulting.yml")
	if _, err := os.Stat(swarmYML); err != nil {
		return fmt.Errorf("adulting swarm manifest not found at %s: %w", swarmYML, err)
	}
	s.swarmDir = filepath.Join(repoRoot, "examples", "swarms", "adulting")
	return nil
}

func (s *adultingSwarmState) theAdultingAgentManifestsAreLoaded() error {
	agentsDir := filepath.Join(s.swarmDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return fmt.Errorf("cannot read agents dir %s: %w", agentsDir, err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(agentsDir, e.Name()))
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", e.Name(), err)
		}
		fm, err := extractFrontmatter(string(data))
		if err != nil {
			return fmt.Errorf("cannot extract frontmatter from %s: %w", e.Name(), err)
		}
		var parsed map[string]interface{}
		if err := yaml.Unmarshal([]byte(fm), &parsed); err != nil {
			return fmt.Errorf("cannot parse frontmatter from %s: %w", e.Name(), err)
		}
		id, _ := parsed["id"].(string)
		if id == "" {
			return fmt.Errorf("%s: no id in frontmatter", e.Name())
		}
		s.manifests[id] = parsed
	}

	return nil
}

func (s *adultingSwarmState) theAgentManifest(id string) error {
	data, ok := s.manifests[id]
	if !ok {
		return fmt.Errorf("no manifest loaded for agent %q (available: %v)", id, manifestKeys(s.manifests))
	}
	s.currentID = id
	s.currentData = data
	return nil
}

func (s *adultingSwarmState) itShouldHaveSetTo(field, value string) error {
	if s.currentData == nil {
		return fmt.Errorf("no agent manifest selected")
	}
	raw, ok := s.currentData[field]
	if !ok {
		return fmt.Errorf("%s: field %q is not set", s.currentID, field)
	}
	actual := fmt.Sprintf("%v", raw)
	if actual != value {
		return fmt.Errorf("%s: expected %s=%s, got %s", s.currentID, field, value, actual)
	}
	return nil
}

func (s *adultingSwarmState) itsCapabilitiesToolsShouldInclude(tool string) error {
	tools := getToolsList(s.currentData)
	for _, t := range tools {
		if t == tool {
			return nil
		}
	}
	return fmt.Errorf("%s: capabilities.tools %v does not include %q", s.currentID, tools, tool)
}

func (s *adultingSwarmState) itsCapabilitiesToolsShouldNotInclude(tool string) error {
	tools := getToolsList(s.currentData)
	for _, t := range tools {
		if t == tool {
			return fmt.Errorf("%s: capabilities.tools %v should not include %q", s.currentID, tools, tool)
		}
	}
	return nil
}

func (s *adultingSwarmState) itsAlwaysActiveSkillsShouldInclude(skill string) error {
	skills := getAlwaysActiveSkills(s.currentData)
	for _, sk := range skills {
		if sk == skill {
			return nil
		}
	}
	return fmt.Errorf("%s: always_active_skills %v does not include %q", s.currentID, skills, skill)
}

func (s *adultingSwarmState) itsAlwaysActiveSkillsShouldNotInclude(skill string) error {
	skills := getAlwaysActiveSkills(s.currentData)
	for _, sk := range skills {
		if sk == skill {
			return fmt.Errorf("%s: always_active_skills %v should not include %q", s.currentID, skills, skill)
		}
	}
	return nil
}

func extractFrontmatter(content string) (string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", fmt.Errorf("no frontmatter delimiter")
	}
	parts := strings.SplitN(content[3:], "---", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("missing closing ---")
	}
	return strings.TrimSpace(parts[0]), nil
}

func getToolsList(data map[string]interface{}) []string {
	return getStringSlice(data, "capabilities", "tools")
}

func getAlwaysActiveSkills(data map[string]interface{}) []string {
	return getStringSlice(data, "capabilities", "always_active_skills")
}

func getStringSlice(data map[string]interface{}, keys ...string) []string {
	current := data
	for i, key := range keys {
		if i == len(keys)-1 {
			raw, ok := current[key]
			if !ok {
				return nil
			}
			items, ok := raw.([]interface{})
			if !ok {
				return nil
			}
			result := make([]string, 0, len(items))
			for _, item := range items {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
		next, ok := current[key]
		if !ok {
			return nil
		}
		current, ok = next.(map[string]interface{})
		if !ok {
			return nil
		}
	}
	return nil
}

func manifestKeys(m map[string]map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func findRepoRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}
