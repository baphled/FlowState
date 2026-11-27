package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/skill"
)

// ParseSkillsScope reads a --skills value: user, project, both or none. The
// empty string is a usage error, unlike ParseHookScope: the CLI applies its
// default, user, at the flag layer, and an app-level request carries an
// already-parsed scope whose zero value stays "not set".
func ParseSkillsScope(s string) (SkillsScope, error) {
	switch SkillsScope(s) {
	case SkillsScopeUser, SkillsScopeProject, SkillsScopeBoth, SkillsScopeNone:
		return SkillsScope(s), nil
	}
	return "", fmt.Errorf("%w: --skills: %q (want user, project, both or none)", ErrUsage, s)
}

// UserSkillsPath is flowstate/skills in the OS user config directory.
func UserSkillsPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("%w: cannot locate the user config directory: %w", ErrConfigInvalid, err)
	}
	return filepath.Join(cfg, "flowstate", "skills"), nil
}

// ProjectSkillsPath is .flowstate/skills below the project root; an empty root
// is the process's current directory. Callers pass the request's ProjectRoot,
// never its Workdir: project skills resolve against the project root, like the
// project hook file, whatever directory the tools run in.
func ProjectSkillsPath(projectRoot string) string {
	if projectRoot == "" {
		projectRoot = "."
	}
	return filepath.Join(projectRoot, ".flowstate", "skills")
}

// SkillsRoots lists the directories a load reads, in order: the user directory
// when the scope is user or both, the project directory when project or both,
// then each explicit path. A directory the scope does not select is never
// opened, so a repository cannot make its skills load by existing. A scope
// outside the four values (the empty one included) is config_invalid.
func SkillsRoots(req SkillsRequest) ([]skill.Root, error) {
	switch req.Scope {
	case SkillsScopeUser, SkillsScopeProject, SkillsScopeBoth, SkillsScopeNone:
	default:
		return nil, fmt.Errorf("%w: --skills scope %q (want user, project, both or none)", ErrConfigInvalid, req.Scope)
	}
	roots := []skill.Root{}
	if req.Scope == SkillsScopeUser || req.Scope == SkillsScopeBoth {
		dir, err := UserSkillsPath()
		if err != nil {
			return nil, err
		}
		roots = append(roots, skill.Root{Dir: dir, Source: skill.SourceUser})
	}
	if req.Scope == SkillsScopeProject || req.Scope == SkillsScopeBoth {
		roots = append(roots, skill.Root{Dir: ProjectSkillsPath(req.ProjectRoot), Source: skill.SourceProject})
	}
	seen := map[string]bool{}
	for _, r := range roots {
		seen[filepath.Clean(r.Dir)] = true
	}
	for _, p := range req.Paths {
		if !seen[filepath.Clean(p)] { // X and X/ are one directory
			seen[filepath.Clean(p)] = true
			roots = append(roots, skill.Root{Dir: p, Source: skill.SourcePath})
		}
	}
	return roots, nil
}

// LoadSkills loads the request's directories (see SkillsRoots), each scanned one
// level deep and reported as its source. A skill that fails validation is in
// the report, never an error here; only an invalid scope or an unlocatable
// user directory is config_invalid.
func LoadSkills(req SkillsRequest) (SkillReport, error) {
	roots, err := SkillsRoots(req)
	if err != nil {
		return SkillReport{}, err
	}
	res := skill.Load(roots)
	report := SkillReport{Skills: res.Skills, Diagnostics: res.Diagnostics,
		Counts: SkillCounts{Roots: len(roots), Loaded: len(res.Skills)}}
	for _, d := range res.Diagnostics {
		if d.Severity == skill.SeverityError {
			report.Counts.Errors++
		} else {
			report.Counts.Warnings++
		}
	}
	return report, nil
}

// ListSkills is LoadSkills without the diagnostics: `skills list` shows what
// loaded and the counts, and `skills validate` shows why the rest did not.
func ListSkills(req SkillsRequest) (SkillReport, error) {
	report, err := LoadSkills(req)
	report.Diagnostics = []skill.Diagnostic{}
	return report, err
}
