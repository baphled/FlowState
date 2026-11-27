package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/app"
)

// routeSkills handles `flowstate skills list|validate [dir...]`: the --skills scope's directories and the
// --skills-path (else skills_paths) roots, each scanned one level deep; a directory argument replaces them all.
// The output is always JSON (--json is ignored). list exits 0 whatever it found; validate exits 4 when any
// skill failed. --skills-default is checked for its shape only.
func routeSkills(args []string, stdout, stderr *os.File) int {
	if len(args) < 1 || (args[0] != "list" && args[0] != "validate") {
		writeError(stderr, fmt.Errorf("%w: expected skills list or skills validate [dir...]", errUsageCmd))
		return exitUsage
	}
	fl, dirs, err := parseSkillsFlags(args[1:])
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errUsageCmd, err))
		return exitUsage
	}
	if code, done := applySessionConfig(fl, stderr); done {
		return code
	}
	req, rerr := skillsRequest(fl, dirs)
	if rerr != nil {
		writeError(stderr, rerr)
		return exitUsage
	}
	load := app.LoadSkills
	if args[0] == "list" {
		load = app.ListSkills
	}
	report, err := load(req)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, report); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if args[0] == "validate" && report.Counts.Errors > 0 {
		return exitConfigInvalid
	}
	return exitOK
}

// skillsRequest is what skills list|validate read; directory arguments replace the settings.
func skillsRequest(fl *flags, dirs []string) (app.SkillsRequest, error) {
	if len(dirs) > 0 {
		if fl.skillsSet || len(fl.skillsPaths) > 0 || len(fl.skillsDefault) > 0 {
			return app.SkillsRequest{}, fmt.Errorf("%w: a directory argument replaces the --skills settings: give one or the other",
				errUsageCmd)
		}
		return app.SkillsRequest{Scope: app.SkillsScopeNone, Paths: dirs}, nil
	}
	req := app.SkillsRequest{Scope: skillsScope(fl), Paths: effectiveSkills(fl).Paths}
	if root, err := filepath.Abs("."); err == nil {
		req.ProjectRoot = root // report project skills by absolute path
	}
	return req, nil
}

// parseSkillsFlags reads the skills settings flags and --json from the
// arguments of skills list|validate (`--flag value` or `--flag=value`); every
// other argument is a directory to scan, and a directory name may not start
// with -.
func parseSkillsFlags(args []string) (*flags, []string, error) {
	fl, dirs := &flags{}, []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
		case isSkillsFlag(a) || a == "--config" || strings.HasPrefix(a, "--config="):
			take := func() (string, error) {
				i++
				if i >= len(args) {
					return "", fmt.Errorf("flag %s requires a value", a)
				}
				return args[i], nil
			}
			v, verr := flagValue(a, take)
			if verr != nil {
				return nil, nil, verr
			}
			if strings.HasPrefix(a, "--config") {
				fl.configPath = v
			} else if serr := fl.setSkillsFlag(a, v); serr != nil {
				return nil, nil, serr
			}
		case strings.HasPrefix(a, "-"):
			return nil, nil, fmt.Errorf("unexpected argument %q", a)
		default:
			dirs = append(dirs, a)
		}
	}
	return fl, dirs, nil
}
