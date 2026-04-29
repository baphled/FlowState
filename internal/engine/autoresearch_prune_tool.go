// Package engine's autoresearch_prune_tool.go defines AutoresearchPruneTool —
// an engine tool that prunes old autoresearch runs from the coordination store.
// Agents invoke it with optional older_than, all, and dry_run parameters; the
// tool returns a JSON summary of what was (or would be) deleted.
//
// The tool satisfies the tool.Tool interface and delegates execution to the
// AutoresearchPruner seam, which internal/app wires to the cli package.

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/baphled/flowstate/internal/runner"
	"github.com/baphled/flowstate/internal/tool"
)

// defaultPruneOlderThan is the default age threshold used when older_than is
// not supplied by the caller.
const defaultPruneOlderThan = 168 * time.Hour // 7 days

// AutoresearchPruneTool implements tool.Tool. It prunes old autoresearch runs
// from the coordination store and returns a JSON summary.
type AutoresearchPruneTool struct {
	pruner runner.AutoresearchPruner
}

// NewAutoresearchPruneTool creates a new AutoresearchPruneTool.
//
// Expected:
//   - pruner is a non-nil AutoresearchPruner (typically wired by internal/app).
//
// Returns:
//   - A configured AutoresearchPruneTool.
//
// Side effects:
//   - None.
func NewAutoresearchPruneTool(pruner runner.AutoresearchPruner) *AutoresearchPruneTool {
	return &AutoresearchPruneTool{pruner: pruner}
}

// Name returns the tool name.
//
// Returns:
//   - The string "autoresearch_prune".
//
// Side effects:
//   - None.
func (t *AutoresearchPruneTool) Name() string { return "autoresearch_prune" }

// CanDelegate reports whether this tool can be used in a delegation context.
//
// Returns:
//   - true — the tool is safe to expose to delegate agents.
//
// Side effects:
//   - None.
func (t *AutoresearchPruneTool) CanDelegate() bool { return true }

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *AutoresearchPruneTool) Description() string {
	return "Prune old autoresearch runs from the coordination store. " +
		"Pass dry_run=true to preview what would be deleted without making changes. " +
		"Pass all=true to prune every run regardless of age. " +
		"Pass older_than as a Go duration string (e.g. '168h') to prune runs older than that threshold (default '168h')."
}

// Schema returns the JSON schema for the tool input.
//
// Returns:
//   - A tool.Schema with optional parameters.
//
// Side effects:
//   - None.
func (t *AutoresearchPruneTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"older_than": {
				Type:        "string",
				Description: "Prune runs older than this Go duration string, e.g. '168h' (default '168h' = 7 days). Ignored when all=true.",
			},
			"all": {
				Type:        "boolean",
				Description: "Prune all runs regardless of age. Overrides older_than. Default false.",
			},
			"dry_run": {
				Type:        "boolean",
				Description: "Report what would be pruned without deleting anything. Default false.",
			},
		},
		Required: []string{},
	}
}

// pruneOutput is the JSON shape returned by Execute.
type pruneOutput struct {
	RunsPruned  int      `json:"runs_pruned"`
	KeysDeleted int      `json:"keys_deleted"`
	DryRun      bool     `json:"dry_run"`
	Runs        []string `json:"runs"`
}

// Execute validates inputs, builds opts, and calls the pruner.
//
// Expected:
//   - ctx is a valid context.
//   - input.Arguments may contain "older_than" (string), "all" (bool), and
//     "dry_run" (bool). All fields are optional.
//
// Returns:
//   - A tool.Result containing the prune summary JSON.
//   - An error if the pruner has not been wired or the prune operation fails.
//
// Side effects:
//   - Deletes coord-store keys for matched runs when dry_run is false.
func (t *AutoresearchPruneTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	if t.pruner == nil {
		return tool.Result{}, errors.New("autoresearch_prune: pruner not configured")
	}

	opts := runner.AutoresearchPruneOpts{
		OlderThan: defaultPruneOlderThan,
	}

	if olderThanStr, ok := input.Arguments["older_than"].(string); ok && olderThanStr != "" {
		d, err := time.ParseDuration(olderThanStr)
		if err != nil {
			return tool.Result{}, errors.New("autoresearch_prune: invalid older_than duration: " + err.Error())
		}
		opts.OlderThan = d
	}

	if all, ok := input.Arguments["all"].(bool); ok {
		opts.All = all
	}

	if dryRun, ok := input.Arguments["dry_run"].(bool); ok {
		opts.DryRun = dryRun
	}

	res, err := t.pruner.PruneAutoresearch(ctx, opts)
	if err != nil {
		return tool.Result{}, err
	}

	runs := res.Runs
	if runs == nil {
		runs = []string{}
	}

	out, err := json.Marshal(pruneOutput{
		RunsPruned:  res.RunsPruned,
		KeysDeleted: res.KeysDeleted,
		DryRun:      res.DryRun,
		Runs:        runs,
	})
	if err != nil {
		return tool.Result{}, err
	}

	return tool.Result{Output: string(out)}, nil
}
