// Package skill discovers and validates skills (ADR-013): directories that hold
// a SKILL.md, a YAML frontmatter header with a markdown body, plus optional
// supporting files. The package is pure and filesystem-only: every root is
// opened with os.OpenRoot so no path or symlink can leave it, and the caller
// supplies the root directories.
package skill

import "time"

// Source names where a skill root came from.
type Source string

// Severity ranks a Diagnostic.
type Severity string

// Skill sources, severities and the limits a skill must respect.
const (
	// SourceUser marks the per-user skills directory.
	SourceUser Source = "user"
	// SourceProject marks the project skills directory below the project root.
	SourceProject Source = "project"
	// SourcePath marks an explicitly named directory.
	SourcePath Source = "path"

	// SeverityError marks a skill that was not loaded.
	SeverityError Severity = "error"
	// SeverityWarning marks a skill that was loaded with a caveat.
	SeverityWarning Severity = "warning"

	// FileName is the file that makes a directory a skill.
	FileName = "SKILL.md"
	// CloseFence is the line that ends a skill.load result: no body may contain it.
	CloseFence = "<<<end skill>>>"
	// MaxFileBytes is the largest SKILL.md, or supporting file, accepted raw.
	MaxFileBytes = 32 << 10
	// MaxFileLines is the most lines a SKILL.md, or supporting file, may have.
	MaxFileLines = 1000
	// MaxResultBytes is the ceiling of a JSON-marshalled, framed skill result.
	MaxResultBytes = 50 << 10
	// MaxNameChars is the longest skill name.
	MaxNameChars = 64
	// MaxDescriptionChars is the longest description.
	MaxDescriptionChars = 500
	// MaxSupportingScan is the most files below one skill that are checked.
	MaxSupportingScan = 1000
	// frameReserve is the room kept in MaxResultBytes for the fence lines the
	// skill.load tool wraps around a body (header, stance and closing line).
	frameReserve = 512
)

// Root is one directory to scan, one level deep: <Dir>/<name>/SKILL.md.
type Root struct {
	// Dir is the directory holding the skill directories.
	Dir string
	// Source says where the directory came from.
	Source Source
}

// Skill is one validated skill.
type Skill struct {
	// Name is the unique skill name; it equals the directory name.
	Name string `json:"name"`
	// Description says what the skill is for; it is never empty.
	Description string `json:"description"`
	// Source is the class of root the skill was found in.
	Source Source `json:"source"`
	// Path is the SKILL.md file.
	Path string `json:"path"`
	// Body is the markdown after the frontmatter, trimmed.
	Body string `json:"-"`
	// SHA256 is the hex sha256 of the whole SKILL.md as read (ADR-013 D17): what a skill.loaded record is held to
	// and the cache keeps. It is never printed (`skills list` shows no hash), so it is left out of the JSON.
	SHA256 string `json:"-"`
	// Bytes is the size of the whole SKILL.md as read: what the 64 KiB bound on required skills counts.
	Bytes int `json:"-"`
}

// Diagnostic is one finding about a skill directory.
type Diagnostic struct {
	// Severity is error (not loaded) or warning (loaded).
	Severity Severity `json:"severity"`
	// Path is the file or directory the finding is about.
	Path string `json:"path"`
	// Message says what is wrong.
	Message string `json:"message"`
}

// Result is the outcome of loading roots: the skills that loaded, sorted by
// name, and every diagnostic in discovery order. One bad skill never blocks the
// others.
type Result struct {
	// Skills are the loaded skills, sorted by name.
	Skills []Skill `json:"skills"`
	// Diagnostics are the errors and warnings found.
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Cache remembers each SKILL.md a load read by its path, mtime and size, so a later load of the same roots
// re-reads and re-hashes a file only when that triple changes (ADR-013 D17): an unchanged file costs one stat. It
// holds what the load concluded, a failure included, with a skill's warnings. A rewrite that keeps the size and the
// mtime is the one change it cannot see; skill.load still reads fresh, and Fresh reads past it. The zero Cache is ready; a nil one caches
// nothing. It is not safe for concurrent use.
type Cache struct {
	entries map[string]cacheEntry // by SKILL.md path
}

// cacheEntry is what a load concluded of one SKILL.md at one (mtime, size).
type cacheEntry struct {
	mtime  time.Time
	size   int64
	skill  Skill
	diags  []Diagnostic
	failed error
}
