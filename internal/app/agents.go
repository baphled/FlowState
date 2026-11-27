package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/tool"
)

// projectAgentsDir is the project profile directory below the working directory.
var projectAgentsDir = filepath.Join(".flowstate", "agents")

// AgentToolNames returns the tool names a profile may list: the session
// catalog (echo, fs.read, fs.write, fs.edit, exec.run, skill.load) plus the
// reserved delegate name. Listing exec.run is valid whether or not its environment
// opt-in is set; the opt-in is checked when a loop starts. A profile that lists
// skill.load has the tool whatever its offer (with an empty one every load is
// skill_not_offered); a root offered none does not register it, and a loop whose
// allowlist names it then fails with a usage error naming the cause.
func AgentToolNames() []string {
	return []string{tool.Echo, tool.FSReadName, tool.FSWriteName, tool.FSEditName, tool.ExecRunName, agent.SkillLoadTool, agent.DelegateTool}
}

// LoadProfiles loads agent profiles. scope is user (also when empty), project
// or both; anything else is a usage error. The user directory is
// os.UserConfigDir()/flowstate/agents, which honours XDG_CONFIG_HOME on Linux:
// that is the operating-system convention, not FlowState config discovery. The
// project directory is <workdir>/.flowstate/agents (workdir "" means the
// current directory) and is read only when the scope asks for it, because
// project profiles are repository-controlled prompts that carry tools. A
// profile that fails validation, or a name
// found in both directories under scope both, or a delegates entry naming a
// profile outside the loaded set, is config_invalid. Whether a profile's skills
// name a loaded skill is checked where that set is known (checkProfileSkills).
func LoadProfiles(scope, workdir string) ([]agent.Profile, error) {
	sc, err := agent.ParseScope(scope)
	if err != nil {
		return nil, fmt.Errorf("%w: --agents: %w", ErrUsage, err)
	}
	userDir := ""
	if sc != agent.ScopeProject {
		cfg, cerr := os.UserConfigDir()
		if cerr != nil {
			return nil, fmt.Errorf("%w: cannot locate the user config directory: %w", ErrConfigInvalid, cerr)
		}
		userDir = filepath.Join(cfg, "flowstate", "agents")
	}
	if workdir == "" {
		workdir = "."
	}
	profiles, err := agent.Load(sc, userDir, filepath.Join(workdir, projectAgentsDir), AgentToolNames())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfigInvalid, err)
	}
	if err := agent.CheckDelegates(profiles); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfigInvalid, err)
	}
	return profiles, nil
}

// ValidateAgentFile checks one profile file against the session tool catalog.
// A failure is config_invalid.
func ValidateAgentFile(path string) error {
	if _, err := agent.LoadFile(path, agent.SourceUser, AgentToolNames()); err != nil {
		return fmt.Errorf("%w: %w", ErrConfigInvalid, err)
	}
	return nil
}
