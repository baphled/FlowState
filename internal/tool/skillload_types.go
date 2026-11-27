package tool

// skill.load (ADR-013 D8) and the port it reads skills through. The tool domain imports no skill adapter:
// the composition hands it the session's offered skills and the skill format's rules as values.

// SkillLoadName is the skill.load tool name.
const SkillLoadName = "skill.load"

// Skill error codes surfaced in tool.result events.
const (
	// ErrCodeSkillNotOffered maps ErrSkillNotOffered.
	ErrCodeSkillNotOffered = "skill_not_offered"
	// ErrCodeSkillNotFound maps ErrSkillNotFound.
	ErrCodeSkillNotFound = "skill_not_found"
	// ErrCodeSkillOversize maps ErrSkillOversize.
	ErrCodeSkillOversize = "skill_oversize"
	// ErrCodeSkillRefused maps ErrSkillRefused.
	ErrCodeSkillRefused = "skill_refused"
)

// SkillFinder resolves the name of a skill the session offers to the skills root holding it: the skill's own directory
// is then opened through os.Root over that root, which judges it (a directory replaced by a symlink out is refused). Any
// error for an unknown or not offered name becomes ErrSkillNotOffered; the message never reaches the model beyond the code.
type SkillFinder func(name string) (root string, err error)

// SkillFormat is how the skill package judges a file at load, supplied by the composition so the tool applies
// the rules discovery applied (ADR-013 D2, D8). A zero function is never called for a file that needs it.
type SkillFormat struct {
	// MaxBytes caps a supporting file raw; a file over it is refused, never truncated.
	MaxBytes int
	// MaxLines is the most lines a supporting file may have.
	MaxLines int
	// MaxResultBytes is the ceiling of the JSON-marshalled, framed result.
	MaxResultBytes int
	// CloseFence is the line that ends every result; no file may contain it.
	CloseFence string
	// ValidPath judges the "/"-separated relative path of a supporting file.
	ValidPath func(rel string) error
	// ValidText refuses a supporting file that is not UTF-8 or holds a control, bidirectional or tag character.
	ValidText func(data []byte) error
	// Body validates a freshly read SKILL.md as discovery did and returns its name and body without the frontmatter.
	Body func(data []byte) (name, body string, err error)
}

// SkillCatalog is the skills a session offers and the format they are read under.
type SkillCatalog struct {
	// Finder resolves an offered name to the skills root holding it; nil offers nothing.
	Finder SkillFinder
	// Format is the skill format the tool enforces.
	Format SkillFormat
	// None marks a catalog that offers no skill (ADR-013 D6): a profile that lists skill.load keeps the tool over it,
	// every load is ErrSkillNotOffered, and the definition the model reads says so (SkillOfferer, SkillNoneDescription)
	// instead of describing a load that cannot succeed. The Finder still decides every name.
	None bool
}

// SkillOfferer is implemented by the skill.load tool: Offered reports whether the session offers any skill, so a
// composer of the tool definitions can tell the model when every load will be refused.
type SkillOfferer interface {
	// Offered reports whether the catalog offers at least one skill.
	Offered() bool
}

// LoadedSkill is what one successful skill.load delivered: the fields of the engine's skill.loaded record.
type LoadedSkill struct {
	// Name is the skill name.
	Name string `json:"name"`
	// File is the supporting file's relative path, empty for SKILL.md.
	File string `json:"file"`
	// SHA256 is the hex hash of the whole file as read.
	SHA256 string `json:"sha256"`
	// Path is the file's path relative to the skills root, never absolute.
	Path string `json:"path"`
	// Bytes is the size of the whole file.
	Bytes int `json:"bytes"`
}

// SkillResult is the outcome of one skill.load. It marshals to the fenced text the model is shown and to
// nothing else, so no path reaches the model (ADR-013 D7, D8); the engine reads LoadedSkill for skill.loaded.
type SkillResult struct {
	text string
	LoadedSkill
}

type skillLoadTool struct {
	catalog SkillCatalog
}

// SkillNoneDescription is what the model reads of skill.load when the session offers no skill: it learns from the
// definition, not from a refusal, that no load can succeed (ADR-013 D6).
const SkillNoneDescription = "No skill is offered in this session: every load is refused."
