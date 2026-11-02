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

// topLevelCommand handles --version/--help entry points; it reports the
// exit code and whether the invocation is fully handled.
func topLevelCommand(arg string, stdout, stderr *os.File) (int, bool) {
	switch arg {
	case "--version":
		_, _ = fmt.Fprintf(stdout, "flowstate %s\n", Version)
		return exitOK, true
	case "--help", "-h", "help":
		usage(stdout)
		return exitOK, true
	}
	return 0, false
}

// positionalID validates subcommand positional arguments and returns
// the session id ("" when none).
func positionalID(sub string, rest []string) (string, error) {
	if sub == "new" || sub == "list" {
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

// Error reports the usage sentinel's message.
func (usageSentinel) Error() string { return "usage" }

// Is reports equality with the session package usage sentinel.
func (usageSentinel) Is(target error) bool { return target.Error() == "usage" }

// dispatch routes one CLI invocation and returns its exit code.
func dispatch(args []string, stdout, stderr *os.File) int {
	if len(args) < 1 {
		writeError(stderr, fmt.Errorf("%w: missing command", errUsageCmd))
		return exitUsage
	}
	if exit, done := topLevelCommand(args[0], stdout, stderr); done {
		return exit
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
		writeError(stderr, fmt.Errorf("%w: %v", errUsageCmd, err))
		return exitUsage
	}
	id, uerr := positionalID(args[1], rest)
	if uerr != nil {
		writeError(stderr, uerr)
		return exitUsage
	}
	st, err := app.OpenStore(fl.db)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: cannot open database %q: %v", app.ErrConfigInvalid, fl.db, err))
		return exitConfigInvalid
	}
	defer st.Close()
	ctx := context.Background()
	switch args[1] {
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
		writeError(stderr, fmt.Errorf("%w: unknown session subcommand %q", errUsageCmd, args[1]))
		return exitUsage
	}
}

// usage prints the CLI help text.

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
		if a == "--json" {
			fl.json = true
			continue
		}
		if !strings.HasPrefix(a, "--") {
			positional = append(positional, a)
			continue
		}
		v, err := flagValue(a, take)
		if err != nil {
			return nil, nil, err
		}
		if serr := fl.setArg(a, v); serr != nil {
			return nil, nil, serr
		}
	}
	return fl, positional, nil
}

// setArg assigns any value-taking flag (string, list or integer) by
// exact or prefixed name; unknown flags are usage errors.
func (f *flags) setArg(name, v string) error {
	switch {
	case name == "--db":
		f.db = v
	case name == "--tool", name == "--allow":
		if name == "--allow" {
			f.allow = append(f.allow, v)
		} else {
			f.tool = v
		}
	case strings.HasPrefix(name, "--status"):
		f.status = v
	case strings.HasPrefix(name, "--provider"):
		f.provider = v
	case strings.HasPrefix(name, "--model"):
		f.model = v
	case strings.HasPrefix(name, "--task"):
		f.task = v
	case name == "--tool-arg" || name == "--tool-arg=json.RawMessage":
		f.toolArg = v
	case name == "--allow-file":
		f.allowFile = v
	case name == "--budget", name == "--max-tokens":
		return parseNamedLimit(f, name, v)
	default:
		return fmt.Errorf("unknown flag %s", name)
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

// limitValue parses one non-negative integer flag value (--budget,
// --max-tokens); non-numeric or negative input is a usage error.
func limitValue(name, v string) (int64, error) {
	n, perr := strconv.ParseInt(v, 10, 64)
	if perr != nil {
		return 0, fmt.Errorf("flag %s expects an integer, got %q", name, v)
	}
	if n < 0 {
		return 0, fmt.Errorf("flag %s must not be negative, got %d", name, n)
	}
	return n, nil
}

// parseNamedLimit sets one named value flag on fl: --allow-file takes
// a path, --budget/--max-tokens take non-negative integers.
func parseNamedLimit(fl *flags, name, v string) error {
	switch name {
	case "--allow-file":
		fl.allowFile = v
	case "--budget":
		n, lerr := limitValue(name, v)
		if lerr != nil {
			return lerr
		}
		fl.budget = n
	default:
		n, lerr := limitValue(name, v)
		if lerr != nil {
			return lerr
		}
		fl.maxTokens = n
	}
	return nil
}

// usage prints the CLI help text.
func usage(w *os.File) {
	_, _ = fmt.Fprint(w, `usage: flowstate [--version] session <subcommand> [flags] [id]

session new    --provider <p> --model <m> --task <text|@file> [--db path]
session list   [--status <s>] [--db path]
session run    <id> [--budget n] [--max-tokens n] [--db path]
session events <id> [--db path]
session close  <id> [--db path]
session tool   <id> --tool <name> [--tool-arg <json>] [--allow <name>]... [--allow-file <manifest>] [--db path]

common flags: --db <path> (default ./flowstate.db), --json
`)
}
