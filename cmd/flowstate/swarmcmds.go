package main

import (
	"context"
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/app"
)

// routeSwarm routes the `swarm` command surface.
func routeSwarm(ctx context.Context, args []string, stdout, stderr *os.File) int {
	if len(args) < 2 {
		writeError(stderr, fmt.Errorf("%w: missing swarm subcommand", errUsageCmd))
		return exitUsage
	}
	argv, execute := args[2:], false
	if args[1] == "run" {
		var err error
		if argv, execute, err = withoutExecute(argv); err != nil {
			writeError(stderr, err)
			return exitUsage
		}
	}
	fl, rest, err := parseFlags(argv)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errUsageCmd, err))
		return exitUsage
	}
	switch args[1] {
	case "validate":
		return swarmValidate(fl, rest, stdout, stderr)
	case "run":
		if execute {
			return swarmRunExecute(ctx, fl, rest, stdout, stderr)
		}
		if code, done := refuseExecuteOnlyFlags(fl, stderr); done {
			return code
		}
		return swarmRun(ctx, fl, rest, stdout, stderr)
	default:
		writeError(stderr, fmt.Errorf("%w: unknown swarm subcommand %q", errUsageCmd, args[1]))
		return exitUsage
	}
}

func swarmValidate(fl *flags, rest []string, stdout, stderr *os.File) int {
	path := fl.manifestPath
	if path == "" && len(rest) < 1 {
		writeError(stderr, fmt.Errorf("%w: swarm validate takes one manifest path", errUsageCmd))
		return exitUsage
	}
	if path == "" {
		path = rest[0]
	}
	m, err := app.ValidateSwarmManifest(path)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", app.ErrUsage, err))
		return exitUsage
	}
	if err := writeJSON(stdout, map[string]any{"name": m.Name, "valid": true}); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}

func swarmRun(ctx context.Context, fl *flags, rest []string, stdout, stderr *os.File) int {
	manifestPath := fl.manifestPath
	if manifestPath == "" && len(rest) < 1 {
		writeError(stderr, fmt.Errorf("%w: swarm run takes one manifest path", errUsageCmd))
		return exitUsage
	}
	if manifestPath == "" {
		manifestPath = rest[0]
	}
	if fl.parent == "" || fl.budget <= 0 {
		writeError(stderr, fmt.Errorf("%w: swarm run requires --parent and --budget", app.ErrUsage))
		return exitUsage
	}
	st, err := openStore(fl.db)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: cannot open database %q: %v", app.ErrConfigInvalid, fl.db, err))
		return exitConfigInvalid
	}
	defer st.Close()
	parent, err := loadRunnableSession(ctx, st, fl.parent)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	res, err := app.RunSwarmFile(ctx, st, parent, manifestPath, fl.budget)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	children := make([]map[string]any, 0, len(res.Children))
	for _, c := range res.Children {
		children = append(children, map[string]any{
			"packet":    c.PacketID,
			"session":   c.Session.ID,
			"status":    c.Session.Status,
			"parent_id": c.Session.ParentID,
		})
	}
	if err := writeJSON(stdout, map[string]any{
		"manifest": res.Manifest.Name,
		"admitted": res.Admitted,
		"accepted": res.Gates.Accepted,
		"children": children,
	}); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}
