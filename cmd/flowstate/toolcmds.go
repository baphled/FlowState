package main

// CLI adapter for `flowstate session tool <id>`: parses flags into a
// ToolRequest, delegates to app.RunSessionTool and maps errors to
// the CLI exit codes. All behaviour lives in internal/app.

import (
	"context"
	"errors"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// cmdTool implements `flowstate session tool <id>`.
//
// Exit codes: 0 success or a recorded tool error outcome; 1 unknown
// session; 2 usage (unknown tool, denied tool, bad manifest); 6
// terminal session.
func cmdTool(ctx context.Context, dbPath string, fl *flags, id string, stdout, stderr *os.File) int {
	outcome, err := app.RunSessionToolFromPath(ctx, dbPath, app.ToolRequest{
		SessionID: id,
		Tool:      fl.tool,
		ToolArg:   fl.toolArg,
		Workdir:   fl.workdir,
		Allow:     fl.allow,
		AllowFile: fl.allowFile,
	})
	if err != nil {
		writeError(stderr, err)
		if errors.Is(err, app.ErrUsage) {
			return exitUsage
		}
		return exitCodeFor(err)
	}
	payload := map[string]any{"session_id": outcome.SessionID, "tool": outcome.Tool, "ok": true, "result": outcome.Result}
	if err := writeJSON(stdout, payload); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}
