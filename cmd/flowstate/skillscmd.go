package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/baphled/flowstate/internal/app"
)

// routeSkills handles `flowstate skills list|validate [dir...]`. With no
// directory the user skills directory is read; each directory named is scanned
// one level deep. The output is always JSON: --json is accepted and ignored, as
// for agents. list exits 0 whatever it found; validate exits 4 (config_invalid)
// when any skill failed, since an invalid skill is a configuration fault.
func routeSkills(args []string, stdout, stderr *os.File) int {
	if len(args) < 1 || (args[0] != "list" && args[0] != "validate") {
		writeError(stderr, fmt.Errorf("%w: expected skills list or skills validate [dir...]", errUsageCmd))
		return exitUsage
	}
	dirs := withoutJSON(args[1:])
	for _, d := range dirs {
		if strings.HasPrefix(d, "-") {
			writeError(stderr, fmt.Errorf("%w: unexpected argument %q (a directory name may not start with -)", errUsageCmd, d))
			return exitUsage
		}
	}
	load := app.LoadSkills
	if args[0] == "list" {
		load = app.ListSkills
	}
	report, err := load(dirs)
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
