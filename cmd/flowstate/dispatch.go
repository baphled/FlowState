package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/baphled/flowstate/internal/app"
)

// defaultDBPath is the SQLite database used when --db is absent.
const defaultDBPath = "flowstate.db"

// dispatch routes one CLI invocation and returns its exit code.
func dispatch(args []string, stdout, stderr *os.File) int {
	if len(args) < 1 {
		writeError(stderr, fmt.Errorf("%w: missing command", errUsageCmd))
		return exitUsage
	}
	if globalCommand(args, stdout) {
		return exitOK
	}
	if args[0] != "session" {
		writeError(stderr, fmt.Errorf("%w: unknown command %q", errUsageCmd, args[0]))
		return exitUsage
	}
	if len(args) < 2 {
		writeError(stderr, fmt.Errorf("%w: missing session subcommand", errUsageCmd))
		return exitUsage
	}
	fl, rest, err := parseFlags(args[2:])
	if err != nil {
		writeError(stderr, err)
		return exitUsage
	}
	id, err := sessionTarget(args[1], rest)
	if err != nil {
		writeError(stderr, err)
		return exitUsage
	}
	st, err := openStore(fl.db)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: cannot open database %q: %v", errConfigInvalid, fl.db, err))
		return exitConfigInvalid
	}
	defer st.Close()
	return runSubcommand(context.Background(), args[1], st, fl, id, stdout, stderr)
}

// runSubcommand dispatches one session subcommand against the open
// store; unknown subcommands are usage errors.
func runSubcommand(ctx context.Context, name string, st *app.Store, fl *flags, id string, stdout, stderr *os.File) int {
	switch name {
	case "new":
		return cmdNew(ctx, st, fl, stdout, stderr)
	case "list":
		return cmdList(ctx, st, fl, stdout, stderr)
	case "run":
		return cmdRun(ctx, st, id, fl, stdout, stderr)
	case "events":
		return cmdEvents(ctx, st, id, stdout, stderr)
	case "close":
		return cmdClose(ctx, st, id, stdout, stderr)
	case "tool":
		return cmdTool(ctx, st, fl, id, stdout, stderr)
	default:
		writeError(stderr, fmt.Errorf("%w: unknown session subcommand %q", errUsageCmd, name))
		return exitUsage
	}
}

// globalCommand handles the global --version and help commands and
// reports whether one matched.
func globalCommand(args []string, stdout *os.File) bool {
	if args[0] == "--version" {
		_, _ = fmt.Fprintf(stdout, "flowstate %s\n", Version)
		return true
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		usage(stdout)
		return true
	}
	return false
}

// sessionTarget validates the positional arguments for subcommand
// name and returns the single optional session id ("" when absent).
func sessionTarget(name string, rest []string) (string, error) {
	if name == "new" || name == "list" {
		if len(rest) > 0 {
			return "", fmt.Errorf("%w: unexpected argument %q", errUsageCmd, rest[0])
		}
		return "", nil
	}
	if len(rest) > 1 {
		return "", fmt.Errorf("%w: unexpected argument %q", errUsageCmd, rest[1])
	}
	if len(rest) == 1 {
		return rest[0], nil
	}
	return "", nil
}

// errUsageCmd wraps the usage sentinel for CLI-level rejections.
var errUsageCmd = usageSentinel{}

// parseBudget validates one --budget/--max-tokens value and records it
// on fl; non-numeric or non-positive values are usage errors.
func parseBudget(fl *flags, name, v string) error {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return fmt.Errorf("flag %s requires a positive integer value", name)
	}
	fl.budget = n
	return nil
}

// parseFlags extracts --db, --json, --status, --provider, --model and
// --task flags; positional arguments are returned in order.
func parseFlags(args []string) (*flags, []string, error) {
	fl := &flags{db: defaultDBPath}
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		take := func() (string, error) {
			i++
			if i >= len(args) {
				return "", fmt.Errorf("flag %s requires a value", a)
			}
			return args[i], nil
		}
		if a == "--db" {
			v, err := take()
			if err != nil {
				return nil, nil, err
			}
			fl.db = v
			continue
		}
		if a == "--json" {
			fl.json = true
			continue
		}
		p, err := parseValueFlag(fl, a, take, positional)
		if err != nil {
			return nil, nil, err
		}
		positional = p
	}
	return fl, positional, nil
}

// parseValueFlag consumes one value-carrying flag and appends any
// positional argument; unknown flags are usage errors.
func parseValueFlag(fl *flags, a string, take func() (string, error), positional []string) ([]string, error) {
	if strings.HasPrefix(a, "--status") || strings.HasPrefix(a, "--provider") || strings.HasPrefix(a, "--model") || strings.HasPrefix(a, "--task") {
		return positional, parseLabeledFlag(fl, a, take)
	}
	if a == "--tool" || a == "--tool-arg" || a == "--tool-arg=json.RawMessage" {
		return positional, parseToolFlag(fl, a, take)
	}
	if a == "--allow" || a == "--allow-file" {
		return positional, parseAllowFlag(fl, a, take)
	}
	switch {
	case a == "--budget" || a == "--max-tokens":
		v, err := take()
		if err != nil {
			return nil, err
		}
		if berr := parseBudget(fl, a, v); berr != nil {
			return nil, berr
		}
	case strings.HasPrefix(a, "--"):
		return nil, fmt.Errorf("unknown flag %s", a)
	default:
		positional = append(positional, a)
	}
	return positional, nil
}

// parseAllowFlag assigns one allowlist flag value to fl.allow or fl.allowFile.
func parseAllowFlag(fl *flags, a string, take func() (string, error)) error {
	v, err := flagValue(a, take)
	if err != nil {
		return err
	}
	if a == "--allow" {
		fl.allow = append(fl.allow, v)
	} else {
		fl.allowFile = v
	}
	return nil
}

// parseToolFlag assigns one tool flag value to fl.tool or fl.toolArg.
func parseToolFlag(fl *flags, a string, take func() (string, error)) error {
	v, err := flagValue(a, take)
	if err != nil {
		return err
	}
	if a == "--tool" {
		fl.tool = v
	} else {
		fl.toolArg = v
	}
	return nil
}

// parseLabeledFlag assigns one labelled value flag (--status,
// --provider, --model, --task) to its field on fl.
func parseLabeledFlag(fl *flags, a string, take func() (string, error)) error {
	v, err := flagValue(a, take)
	if err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(a, "--status"):
		fl.status = v
	case strings.HasPrefix(a, "--provider"):
		fl.provider = v
	case strings.HasPrefix(a, "--model"):
		fl.model = v
	default:
		fl.task = v
	}
	return nil
}

// flagValue resolves either `--flag value` or `--flag=value`.
func flagValue(a string, take func() (string, error)) (string, error) {
	if eq := strings.IndexByte(a, '='); eq >= 0 {
		return a[eq+1:], nil
	}
	return take()
}

// openStore opens (creating directories for) the SQLite database.
func openStore(path string) (*app.Store, error) {
	return app.OpenStore(path)
}

// usage prints the CLI help text.
func usage(w *os.File) {
	_, _ = fmt.Fprint(w, `usage: flowstate [--version] session <subcommand> [flags] [id]

session new    --provider <p> --model <m> --task <text|@file> [--db path]
session list   [--status <s>] [--db path]
session run    <id> [--max-tokens <n>|--budget <cap>] [--db path]
session events <id> [--db path]
session close  <id> [--db path]
session tool   <id> --tool <name> [--tool-arg <json>] [--allow <name>]... [--allow-file <manifest>] [--db path]

common flags: --db <path> (default ./flowstate.db), --json
`)
}
