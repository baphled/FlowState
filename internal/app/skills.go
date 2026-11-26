package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/skill"
)

// LoadSkills loads skills from the given directories, each scanned one level
// deep and reported as source path. With no directory it reads the user
// directory, os.UserConfigDir()/flowstate/skills (which honours XDG_CONFIG_HOME
// on Linux: the operating-system convention, not FlowState config discovery).
// Project directories and the scope and path settings are not read here: they
// arrive with ADR-013's configuration packet. A skill that fails validation is
// in the report, never an error here; only an unlocatable user directory is
// config_invalid.
func LoadSkills(dirs []string) (SkillReport, error) {
	roots := make([]skill.Root, 0, len(dirs)+1)
	for _, d := range dirs {
		roots = append(roots, skill.Root{Dir: d, Source: skill.SourcePath})
	}
	if len(roots) == 0 {
		cfg, err := os.UserConfigDir()
		if err != nil {
			return SkillReport{}, fmt.Errorf("%w: cannot locate the user config directory: %w", ErrConfigInvalid, err)
		}
		roots = append(roots, skill.Root{Dir: filepath.Join(cfg, "flowstate", "skills"), Source: skill.SourceUser})
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
func ListSkills(dirs []string) (SkillReport, error) {
	report, err := LoadSkills(dirs)
	report.Diagnostics = []skill.Diagnostic{}
	return report, err
}
