package tool

import "io/fs"

// fsOptions carries the overridable size caps.
type fsOptions struct {
	maxRead, maxWrite, maxEdit int64
	protect                    *fsProtect
}

// fsProtect holds the protected directories (ADR-012 D20); a nil *fsProtect disables the refusal.
type fsProtect struct {
	dirs []string
}

// FSOption customises an fs tool constructor.
type FSOption func(*fsOptions)

// fsReadTool reads bounded file contents under the workdir.
type fsReadTool struct {
	workdir string
	max     int64
}

// fsWriteTool writes bounded content under the workdir.
type fsWriteTool struct {
	workdir string
	max     int64
	protect *fsProtect
}

// fsEditTool applies bounded string replacements under the workdir.
type fsEditTool struct {
	workdir string
	max     int64
	protect *fsProtect
}

// fsEditArgs is the fs.edit argument payload.
type fsEditArgs struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
	All     bool   `json:"all"`
}

// linkState says how far resolveLink got.
type linkState int

const (
	// linkResolved: the key is the final path (it may not exist).
	linkResolved linkState = iota
	// linkLoop: the hop bound was hit; nothing can be opened or created through the link.
	linkLoop
	// linkUnknown: the key is the deepest directory reached; the final path is unknowable (a read error, or ".."
	// after a missing component). Every write under the key is treated as hit: fail closed, with the smallest
	// subtree that contains whatever the link could name.
	linkUnknown
)

// linkScan is one symlink pass over a protected directory: what a write is checked against, the entries counted
// so far (shared by every directory reached through a link), the resolved directories already walked, and the
// refusal reason once one is found.
type linkScan struct {
	target string
	info   fs.FileInfo
	multi  bool
	n      int
	seen   map[string]bool
	why    string
}
