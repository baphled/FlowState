package app

import "github.com/baphled/flowstate/internal/skill"

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
