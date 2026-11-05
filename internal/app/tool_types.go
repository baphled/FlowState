package app

// ToolRequest carries the CLI-parsed arguments for one tool run.
type ToolRequest struct {
	SessionID string
	Tool      string
	ToolArg   string
	// Workdir is the --workdir override; empty defaults beside the db.
	Workdir string
	Allow   []string
	// AllowFile is the --allow-file manifest path.
	AllowFile string
}

// ToolOutcome is the result of a tool invocation plus the recorded
// outcome classification for the CLI's exit-code mapping.
type ToolOutcome struct {
	SessionID string
	Tool      string
	Result    any
}
