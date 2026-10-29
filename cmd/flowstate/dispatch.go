package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/baphled/flowstate/internal/store"
)

// defaultDBPath is the SQLite database used when --db is absent.
const defaultDBPath = "flowstate.db"

// flags holds the parsed global flags for one invocation.
type flags struct {
	db     string
	json   bool
	status string
	// session new
	provider string
	model    string
	task     string
	// session tool
	tool      string
	toolArg   string
	allow     []string
	allowFile string
	// session run
	budget int64
}

// dispatch routes one CLI invocation and returns its exit code.
func dispatch(args []string, stdout, stderr *os.File) int {
	if len(args) < 1 {
		writeError(stderr, fmt.Errorf("%w: missing command", errUsageCmd))
		return exitUsage
	}
	if args[0] == "--version" {
		_, _ = fmt.Fprintf(stdout, "flowstate %s\n", Version)
		return exitOK
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		usage(stdout)
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
	st, err := openStore(fl.db)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: cannot open database %q: %v", errConfigInvalid, fl.db, err))
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

// errUsageCmd wraps the usage sentinel for CLI-level rejections.
var errUsageCmd = usageSentinel{}

// usageSentinel satisfies errors.Is against session.ErrUsage while
// formatting a usage message.
type usageSentinel struct{}

func (usageSentinel) Error() string { return "usage" }

// Is reports equality with the session package usage sentinel.
func (usageSentinel) Is(target error) bool { return target.Error() == "usage" }

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
		switch {
		case a == "--db":
			v, err := take()
			if err != nil {
				return nil, nil, err
			}
			fl.db = v
		case a == "--json":
			fl.json = true
		case strings.HasPrefix(a, "--status"):
			v, err := flagValue(a, take)
			if err != nil {
				return nil, nil, err
			}
			fl.status = v
		case strings.HasPrefix(a, "--provider"):
			v, err := flagValue(a, take)
			if err != nil {
				return nil, nil, err
			}
			fl.provider = v
		case strings.HasPrefix(a, "--model"):
			v, err := flagValue(a, take)
			if err != nil {
				return nil, nil, err
			}
			fl.model = v
		case strings.HasPrefix(a, "--task"):
			v, err := flagValue(a, take)
			if err != nil {
				return nil, nil, err
			}
			fl.task = v
		case a == "--tool":
			v, err := take()
			if err != nil {
				return nil, nil, err
			}
			fl.tool = v
		case a == "--tool-arg" || a == "--tool-arg=json.RawMessage":
			v, err := flagValue(a, take)
			if err != nil {
				return nil, nil, err
			}
			fl.toolArg = v
		case a == "--allow":
			v, err := flagValue(a, take)
			if err != nil {
				return nil, nil, err
			}
			fl.allow = append(fl.allow, v)
		case a == "--allow-file":
			v, err := flagValue(a, take)
			if err != nil {
				return nil, nil, err
			}
			fl.allowFile = v
		case a == "--budget" || a == "--max-tokens":
			v, err := take()
			if err != nil {
				return nil, nil, err
			}
			if berr := parseBudget(fl, a, v); berr != nil {
				return nil, nil, berr
			}
		case strings.HasPrefix(a, "--"):
			return nil, nil, fmt.Errorf("unknown flag %s", a)
		default:
			positional = append(positional, a)
		}
	}
	return fl, positional, nil
}

// flagValue resolves either `--flag value` or `--flag=value`.
func flagValue(a string, take func() (string, error)) (string, error) {
	if eq := strings.IndexByte(a, '='); eq >= 0 {
		return a[eq+1:], nil
	}
	return take()
}

// openStore opens (creating directories for) the SQLite database.
func openStore(path string) (*store.Store, error) {
	if path != ":memory:" {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return store.Open(path)
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
