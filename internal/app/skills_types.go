package app

import "github.com/baphled/flowstate/internal/skill"

// SkillsScope is the --skills value: which skills directories a load reads.
type SkillsScope string

// The --skills scopes; SkillsScopeUser is the default the CLI applies.
const (
	// SkillsScopeUser reads the user directory only.
	SkillsScopeUser SkillsScope = "user"
	// SkillsScopeProject reads the project directory only.
	SkillsScopeProject SkillsScope = "project"
	// SkillsScopeBoth reads the user directory, then the project directory.
	SkillsScopeBoth SkillsScope = "both"
	// SkillsScopeNone reads neither.
	SkillsScopeNone SkillsScope = "none"
)

// SkillsRequest names the skills directories one load reads (ADR-013 D3, D16).
type SkillsRequest struct {
	// Scope is the already-parsed --skills scope. The zero value is invalid, so a
	// request that never parsed the flag loads nothing rather than the user
	// directory (fail closed); the CLI applies its default, user, itself.
	Scope SkillsScope
	// ProjectRoot is where the project skills directory is found; empty is the
	// process current directory. It is never the per-run workdir, as for the
	// project hook file.
	ProjectRoot string
	// Paths are the explicit directories, absolute, in the order given: the
	// --skills-path flags, else the skills_paths config key. They are read
	// after the scope's directories.
	Paths []string
}

// SkillCounts summarises a skills load.
type SkillCounts struct {
	// Roots is the number of directories scanned.
	Roots int `json:"roots"`
	// Loaded is the number of skills that loaded.
	Loaded int `json:"loaded"`
	// Errors is the number of error diagnostics (a duplicate name is one).
	Errors int `json:"errors"`
	// Warnings is the number of warning diagnostics.
	Warnings int `json:"warnings"`
}

// SkillReport is what `flowstate skills list|validate` prints: the loaded
// skills, every diagnostic and the counts. It carries no file hashes.
type SkillReport struct {
	// Skills are the loaded skills, sorted by name.
	Skills []skill.Skill `json:"skills"`
	// Diagnostics are the errors and warnings found.
	Diagnostics []skill.Diagnostic `json:"diagnostics"`
	// Counts summarises the load.
	Counts SkillCounts `json:"counts"`
}
