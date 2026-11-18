package main

import (
	"context"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// cmdReplay implements `flowstate replay <id>`: verifies the stored
// event log and prints the JSONL transcript. 0 deterministic; 1
// unknown session; 7 replay mismatch; 2 usage.
func cmdReplay(ctx context.Context, st *app.Store, id string, stdout, stderr *os.File) int {
	if id == "" {
		writeError(stderr, fmt.Errorf("%w: session id required", app.ErrUsage))
		return exitUsage
	}
	if _, err := st.GetSession(ctx, id); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	lines, err := st.ExportJSONL(ctx, id)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if _, err := stdout.Write(lines); err != nil {
		writeError(stderr, fmt.Errorf("%w: cannot write transcript: %v", app.ErrConfigInvalid, err))
		return exitConfigInvalid
	}
	return exitOK
}

// cmdReceiptValidate implements `flowstate receipt validate
// <file...>`: per-file verdict JSON lines. 0 all valid; 8 any
// invalid; 2 usage.
func cmdReceiptValidate(paths []string, stdout, stderr *os.File) int {
	if len(paths) == 0 {
		writeError(stderr, fmt.Errorf("%w: at least one receipt file required", app.ErrUsage))
		return exitUsage
	}
	invalid := 0
	for _, p := range paths {
		verdict := "valid"
		if err := app.ValidateReceiptFile(p); err != nil {
			verdict = "invalid"
			invalid++
			writeError(stderr, err)
		}
		if err := writeJSON(stdout, map[string]any{"path": p, "verdict": verdict}); err != nil {
			writeError(stderr, err)
			return exitCodeFor(err)
		}
	}
	if invalid > 0 {
		return exitReceiptInvalid
	}
	return exitOK
}
