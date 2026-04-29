// Package engine's autoresearch_run_tool.go defines AutoresearchRunTool —
// an engine tool that launches an autoresearch run as a background task.
// Agents invoke it with a surface, driver, and evaluator script; the tool
// returns a task_id immediately and the caller polls via background_output.
//
// The tool satisfies the tool.Tool interface and delegates execution to
// the AutoresearchRunner seam, which internal/app wires to the cli package.

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/baphled/flowstate/internal/runner"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/google/uuid"
)

// AutoresearchRunTool implements tool.Tool. It launches an autoresearch
// run as a background task and returns the task_id immediately so the
// calling agent can poll for results via background_output.
type AutoresearchRunTool struct {
	manager *BackgroundTaskManager
	runner  runner.AutoresearchRunner
}

// NewAutoresearchRunTool creates a new AutoresearchRunTool.
//
// Expected:
//   - mgr is a non-nil BackgroundTaskManager.
//   - runner is a non-nil AutoresearchRunner (typically wired by internal/app).
//
// Returns:
//   - A configured AutoresearchRunTool.
//
// Side effects:
//   - None.
func NewAutoresearchRunTool(mgr *BackgroundTaskManager, r runner.AutoresearchRunner) *AutoresearchRunTool {
	return &AutoresearchRunTool{manager: mgr, runner: r}
}

// Name returns the tool name.
//
// Returns:
//   - The string "autoresearch_run".
//
// Side effects:
//   - None.
func (t *AutoresearchRunTool) Name() string { return "autoresearch_run" }

// CanDelegate reports whether this tool can be used in a delegation context.
//
// Returns:
//   - true — the tool is safe to expose to delegate agents.
//
// Side effects:
//   - None.
func (t *AutoresearchRunTool) CanDelegate() bool { return true }

// Description returns a human-readable description of the tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
func (t *AutoresearchRunTool) Description() string {
	return "Launch an autoresearch optimisation run as a background task. " +
		"Returns task_id immediately; poll background_output for results."
}

// Schema returns the JSON schema for the tool input.
//
// Returns:
//   - A tool.Schema with required and optional parameters.
//
// Side effects:
//   - None.
func (t *AutoresearchRunTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"surface": {
				Type:        "string",
				Description: "Path to the surface file to optimise (manifest, skill body, or source file).",
			},
			"driver_script": {
				Type:        "string",
				Description: "Path to the driver script that produces candidate content.",
			},
			"evaluator_script": {
				Type:        "string",
				Description: "Path to the evaluator script that scores candidate content.",
			},
			"run_id": {
				Type:        "string",
				Description: "Optional run identifier; generated if empty.",
			},
			"max_trials": {
				Type:        "integer",
				Description: "Maximum number of trials before terminating (default 10).",
			},
			"time_budget": {
				Type:        "string",
				Description: "Wall-clock budget as a Go duration string, e.g. '5m' (default '5m').",
			},
			"metric_direction": {
				Type:        "string",
				Description: "Score direction: 'min' (lower is better) or 'max' (higher is better).",
				Enum:        []string{"min", "max"},
			},
			"driver_agent": {
				Type:        "string",
				Description: "Agent ID for the driver to use. Sets FLOWSTATE_AUTORESEARCH_DRIVER_AGENT. Empty = driver default.",
			},
		},
		Required: []string{"surface", "driver_script", "evaluator_script"},
	}
}

// Execute validates inputs, builds opts, and launches the run as a
// background task. Returns {"task_id": "<id>", "status": "running"}
// immediately.
//
// Expected:
//   - ctx is a valid context.
//   - input.Arguments contains "surface", "driver_script", and "evaluator_script".
//
// Returns:
//   - A tool.Result containing task_id and status=running.
//   - An error if any required field is missing or empty.
//
// Side effects:
//   - Launches a goroutine via BackgroundTaskManager.Launch.
func (t *AutoresearchRunTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	surface, _ := input.Arguments["surface"].(string)
	driverScript, _ := input.Arguments["driver_script"].(string)
	evaluatorScript, _ := input.Arguments["evaluator_script"].(string)

	if strings.TrimSpace(surface) == "" {
		return tool.Result{}, errors.New("autoresearch_run: surface is required")
	}
	if strings.TrimSpace(driverScript) == "" {
		return tool.Result{}, errors.New("autoresearch_run: driver_script is required")
	}
	if strings.TrimSpace(evaluatorScript) == "" {
		return tool.Result{}, errors.New("autoresearch_run: evaluator_script is required")
	}

	opts := runner.AutoresearchOpts{
		Surface:         surface,
		DriverScript:    driverScript,
		EvaluatorScript: evaluatorScript,
	}

	if runID, ok := input.Arguments["run_id"].(string); ok && runID != "" {
		opts.RunID = runID
	} else {
		opts.RunID = uuid.NewString()
	}

	if maxTrials, ok := input.Arguments["max_trials"]; ok {
		switch v := maxTrials.(type) {
		case float64:
			opts.MaxTrials = int(v)
		case int:
			opts.MaxTrials = v
		}
	}
	if opts.MaxTrials <= 0 {
		opts.MaxTrials = 10
	}

	if timeBudgetStr, ok := input.Arguments["time_budget"].(string); ok && timeBudgetStr != "" {
		if d, parseErr := time.ParseDuration(timeBudgetStr); parseErr == nil {
			opts.TimeBudget = d
		}
	}
	if opts.TimeBudget <= 0 {
		opts.TimeBudget = 5 * time.Minute
	}

	if dir, ok := input.Arguments["metric_direction"].(string); ok && dir != "" {
		opts.MetricDirection = dir
	}

	if driverAgent, ok := input.Arguments["driver_agent"].(string); ok && driverAgent != "" {
		opts.DriverAgent = driverAgent
	}

	taskID := opts.RunID

	fn := func(taskCtx context.Context) (string, error) {
		var buf strings.Builder
		result, runErr := t.runner.RunAutoresearch(taskCtx, opts, &buf)
		if runErr != nil {
			return "", runErr
		}
		out, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(out), nil
	}

	t.manager.Launch(context.WithoutCancel(ctx), taskID, "autoresearch", "autoresearch: "+surface, fn)

	resp, err := json.Marshal(map[string]string{
		"task_id": taskID,
		"status":  "running",
	})
	if err != nil {
		return tool.Result{}, err
	}

	return tool.Result{Output: string(resp)}, nil
}
