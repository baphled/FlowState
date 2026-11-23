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
	return dispatchContexted(context.Background(), args, stdout, stderr)
}

// dispatchContexted routes with a context that may carry the SIGINT
// marker into session subcommands.

// routeTopLevel handles the non-session top-level commands that own
// their full argv routing; routed=false leaves the command unhandled.
func routeTopLevel(ctx context.Context, args []string, stdout, stderr *os.File) (int, bool) {
	switch args[0] {
	case "swarm":
		return routeSwarm(ctx, args, stdout, stderr), true
	case "serve":
		return routeServe(ctx, args, stdout, stderr), true
	case "store":
		return routeStore(args, stdout, stderr)
	case "agents":
		return routeAgents(args[1:], stdout, stderr), true
	}
	return 0, false
}

func dispatchContexted(ctx context.Context, args []string, stdout, stderr *os.File) int {
	if len(args) < 1 {
		writeError(stderr, fmt.Errorf("%w: missing command", errUsageCmd))
		return exitUsage
	}
	if isMetaCommand(args[0], stdout) {
		return exitOK
	}
	if code, routed := routeReplay(ctx, args, stdout, stderr); routed {
		return code
	}
	if code, routed := routeReceipt(args, stdout, stderr); routed {
		return code
	}
	if code, routed := routeTopLevel(ctx, args, stdout, stderr); routed {
		return code
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
	if args[1] == "new" || args[1] == "list" {
		if len(rest) > 0 {
			writeError(stderr, fmt.Errorf("%w: unexpected argument %q", errUsageCmd, rest[0]))
			return exitUsage
		}
	} else if len(rest) > 1 {
		writeError(stderr, fmt.Errorf("%w: unexpected argument %q", errUsageCmd, rest[1]))
		return exitUsage
	}
	id := ""
	if len(rest) == 1 {
		id = rest[0]
	}
	if code, done := applySessionConfig(fl, stderr); done {
		return code
	}
	return dispatchSession(ctx, args[1], fl, id, stdout, stderr)
}

// isMetaCommand handles --version/--help and reports whether the
// invocation was one of them.
func isMetaCommand(arg string, stdout *os.File) bool {
	switch arg {
	case "--version":
		_, _ = fmt.Fprintf(stdout, "flowstate %s\n", Version)
	case "--help", "-h", "help":
		usage(stdout)
	default:
		return false
	}
	return true
}

// routeReplay handles `flowstate replay <id>`; routed reports whether
// the invocation belonged to this route.
func routeReplay(ctx context.Context, args []string, stdout, stderr *os.File) (int, bool) {
	if len(args) < 1 || args[0] != "replay" {
		return exitOK, false
	}
	fl, rest, err := parseFlags(args[1:])
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", errUsageCmd, err))
		return exitUsage, true
	}
	if len(rest) != 1 {
		writeError(stderr, fmt.Errorf("%w: replay requires exactly one session id", errUsageCmd))
		return exitUsage, true
	}
	if code, done := applySessionConfig(fl, stderr); done {
		return code, true
	}
	return dispatchSession(ctx, "replay", fl, rest[0], stdout, stderr), true
}

// routeReceipt handles `flowstate receipt validate <file...>`.
func routeReceipt(args []string, stdout, stderr *os.File) (int, bool) {
	if len(args) < 1 || args[0] != "receipt" {
		return exitOK, false
	}
	if len(args) < 2 || args[1] != "validate" {
		writeError(stderr, fmt.Errorf("%w: expected receipt validate <file...>", errUsageCmd))
		return exitUsage, true
	}
	return cmdReceiptValidate(args[2:], stdout, stderr), true
}

// dispatchSession opens the store and routes one session subcommand.
func dispatchSession(ctx context.Context, sub string, fl *flags, id string, stdout, stderr *os.File) int {
	st, err := openStore(fl.db)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: cannot open database %q: %v", app.ErrConfigInvalid, fl.db, err))
		return exitConfigInvalid
	}
	defer st.Close()
	switch sub {
	case "new":
		return cmdNew(ctx, st, fl, stdout, stderr)
	case "list":
		return cmdList(ctx, st, fl, stdout, stderr)
	case "run":
		return cmdRun(ctx, st, id, fl, stdout, stderr)
	case "events":
		return cmdEvents(ctx, st, id, fl, stdout, stderr)
	case "cancel":
		return cmdCancel(ctx, st, id, stdout, stderr)
	case "replay":
		return cmdReplay(ctx, st, id, stdout, stderr)
	case "close":
		return cmdClose(ctx, st, id, stdout, stderr)
	case "tool":
		return cmdTool(ctx, fl.db, fl, id, stdout, stderr)
	case "budget":
		return cmdBudget(ctx, st, id, stdout)
	case "doctor":
		return cmdDoctor(ctx, st, id, stdout, stderr)
	case "message":
		return cmdMessage(ctx, st, id, fl, stdout, stderr)
	default:
		writeError(stderr, fmt.Errorf("%w: unknown session subcommand %q", errUsageCmd, sub))
		return exitUsage
	}
}

// errUsageCmd wraps the usage sentinel for CLI-level rejections.
var errUsageCmd = usageSentinel{}

// Error reports the usage sentinel's message.
func (usageSentinel) Error() string { return "usage" }

// Is reports equality with the app usage sentinel.
func (usageSentinel) Is(target error) bool { return target.Error() == "usage" }

// parseFlags extracts --db, --json, --status, --provider, --model,
// --task, --parent and --reserve flags; positional arguments are
// returned in order.
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
		switch {
		case a == "--db":
			v, err := take()
			if err != nil {
				return nil, nil, err
			}
			fl.db = v
			fl.dbSet = true
		case a == "--json":
			fl.json = true
		case a == "--follow":
			fl.follow = true
		case strings.HasPrefix(a, "--status"), strings.HasPrefix(a, "--provider"),
			strings.HasPrefix(a, "--model"), strings.HasPrefix(a, "--task"),
			strings.HasPrefix(a, "--parent"), strings.HasPrefix(a, "--config"),
			strings.HasPrefix(a, "--manifest"), a == "--from", a == "--body":
			if serr := fl.setString(a, take); serr != nil {
				return nil, nil, serr
			}
		case strings.HasPrefix(a, "--tool"), a == "--allow", a == "--allow-file", a == "--workdir":
			if serr := fl.setToolFlag(a, take); serr != nil {
				return nil, nil, serr
			}
		case a == "--budget", a == "--max-tokens", a == "--reserve":
			v, err := flagValue(a, take)
			if err != nil {
				return nil, nil, err
			}
			if lerr := setLimit(fl, a, v); lerr != nil {
				return nil, nil, lerr
			}
		case strings.HasPrefix(a, "--"):
			return nil, nil, fmt.Errorf("unknown flag %s", a)
		default:
			positional = append(positional, a)
		}
	}
	return fl, positional, nil
}

// setString assigns one prefixed string flag (--status, --provider,
// --model, --task) by name, resolving `--flag value` or `--flag=value`.
func (f *flags) setString(name string, take func() (string, error)) error {
	v, err := flagValue(name, take)
	if err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(name, "--status"):
		f.status = v
	case strings.HasPrefix(name, "--config"):
		f.configPath = v
	case strings.HasPrefix(name, "--provider"):
		f.provider = v
	case strings.HasPrefix(name, "--model"):
		f.model = v
	case strings.HasPrefix(name, "--task"):
		f.task = v
	case strings.HasPrefix(name, "--parent"):
		f.parent = v
	case name == "--from":
		f.msgFrom = v
	case name == "--body":
		f.msgBody = v
	case strings.HasPrefix(name, "--manifest"):
		f.manifestPath = v
	default:
		return fmt.Errorf("unknown flag %s", name)
	}
	return nil
}

// setToolFlag assigns one tool-related flag (--tool, --tool-arg,
// --allow, --allow-file, --workdir), resolving `--flag value` or
// `--flag=value`. Prefix order matters: --tool-arg and --allow-file
// must be matched before --tool and --allow.
func (f *flags) setToolFlag(name string, take func() (string, error)) error {
	v, err := flagValue(name, take)
	if err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(name, "--tool-arg"):
		f.toolArg = v
	case strings.HasPrefix(name, "--tool"):
		f.tool = v
	case strings.HasPrefix(name, "--allow-file"):
		f.allowFile = v
	case strings.HasPrefix(name, "--allow"):
		f.allow = append(f.allow, v)
	default:
		f.workdir = v
	}
	return nil
}

// setLimit assigns one non-negative integer limit flag (--budget,
// --max-tokens); non-numeric or negative input is a usage error.
func setLimit(fl *flags, name, v string) error {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return fmt.Errorf("flag %s expects a non-negative integer, got %q", name, v)
	}
	switch name {
	case "--budget":
		fl.budget = n
		fl.budgetSet = true
	case "--reserve":
		fl.reserve = n
		fl.reserveSet = true
	default:
		fl.maxTokens = n
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

// openStore opens (creating parent directories for) the SQLite
// database via the composition root, which owns directory creation.
func openStore(path string) (*app.Store, error) {
	return app.OpenStore(path)
}

// usage prints the CLI help text.
func usage(w *os.File) {
	_, _ = fmt.Fprint(w, `usage: flowstate [--version] session <subcommand> [flags] [id]
serve [--addr <host:port>] [--db path] [--config <path>]

session new    --provider <p> --model <m> --task <text|@file> [--parent <id>] [--reserve <n>] [--db path]
session budget <id> [--db path]
session doctor <id> [--db path]
session message <id> --from <parent> --body <text|@file> [--db path]
session list   [--status <s>] [--db path]
session run    <id> [--budget <n>] [--max-tokens <n>] [--allow <name>]... [--allow-file <manifest>] [--workdir <path>] [--db path]
               (--allow/--allow-file replace the config tool_allowlist; --workdir only sets where tools
               run and needs an allowlist from --allow, --allow-file or tool_allowlist)
session events <id> [--follow] [--db path]
session close  <id> [--db path]
session cancel <id> [--db path]
session replay <id> [--db path]
receipt validate <file...>
session tool   <id> --tool <name> [--tool-arg <json>] [--allow <name>]... [--allow-file <manifest>] [--db path]
store info [--db path]
agents list [--agents user|project|both] [--workdir path]
agents validate <file...>

common flags: --db <path> (default ./flowstate.db), --config <path> (default ./flowstate.json), --json
`)
}
