package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/app"
)

// routeAgents handles `flowstate agents list|validate`.
func routeAgents(args []string, stdout, stderr *os.File) int {
	if len(args) < 1 || (args[0] != "list" && args[0] != "validate") {
		writeError(stderr, fmt.Errorf("%w: expected agents list or agents validate <file...>", errUsageCmd))
		return exitUsage
	}
	if args[0] == "validate" {
		return cmdAgentsValidate(args[1:], stdout, stderr)
	}
	scope, workdir, err := parseAgentsFlags(args[1:])
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errUsageCmd, err))
		return exitUsage
	}
	profiles, err := app.LoadProfiles(scope, workdir)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, profiles); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return exitOK
}

// parseAgentsFlags reads --agents and --workdir (`--flag value` or
// `--flag=value`); any other argument is a usage error.
func parseAgentsFlags(args []string) (scope, workdir string, err error) {
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if name != "--agents" && name != "--workdir" {
			return "", "", fmt.Errorf("unexpected argument %q", args[i])
		}
		if !hasValue {
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("flag %s requires a value", name)
			}
			value = args[i]
		}
		if name == "--agents" {
			scope = value
		} else {
			workdir = value
		}
	}
	return scope, workdir, nil
}

// cmdAgentsValidate prints {path, ok, error?} per file. Exit 0 when every file
// is valid, 2 with no files, and 4 (config_invalid) when any file fails: an
// invalid profile is a configuration fault, so no new exit code is added.
func cmdAgentsValidate(paths []string, stdout, stderr *os.File) int {
	if len(paths) == 0 {
		writeError(stderr, fmt.Errorf("%w: at least one profile file required", app.ErrUsage))
		return exitUsage
	}
	invalid := 0
	for _, p := range paths {
		verdict := map[string]any{"path": p, "ok": true}
		if err := app.ValidateAgentFile(p); err != nil {
			invalid++
			verdict["ok"], verdict["error"] = false, err.Error()
		}
		if err := writeJSON(stdout, verdict); err != nil {
			writeError(stderr, err)
			return exitCodeFor(err)
		}
	}
	if invalid > 0 {
		return exitConfigInvalid
	}
	return exitOK
}
