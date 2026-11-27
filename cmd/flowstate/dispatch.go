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
	case "skills":
		return routeSkills(args[1:], stdout, stderr), true
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
	if refuseInapplicableFlag(args, stderr) {
		return exitUsage
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
	if refuseSessionArgs(fl, args[1], rest, stderr) {
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
		return cmdDoctor(ctx, st, id, fl, stdout, stderr)
	case "message":
		return cmdMessage(ctx, st, id, fl, stdout, stderr)
	case "compact":
		return cmdCompact(ctx, st, id, fl, stdout, stderr)
	case "unlock":
		return cmdUnlock(ctx, st, id, fl, stdout, stderr)
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
		case a == "--json", a == "--follow", a == "--force":
			fl.setSwitch(a)
		case strings.HasPrefix(a, "--status"), strings.HasPrefix(a, "--provider"),
			strings.HasPrefix(a, "--model"), strings.HasPrefix(a, "--task"),
			strings.HasPrefix(a, "--parent"), strings.HasPrefix(a, "--config"),
			strings.HasPrefix(a, "--manifest"), strings.HasPrefix(a, "--instructions"), a == "--from", a == "--body":
			if serr := fl.setString(a, take); serr != nil {
				return nil, nil, serr
			}
		case strings.HasPrefix(a, "--tool"), a == "--allow", a == "--allow-file", a == "--workdir", strings.HasPrefix(a, "--agents"),
			a == "--hooks", strings.HasPrefix(a, "--hooks="), isSkillsFlag(a):
			if serr := fl.setToolFlag(a, take); serr != nil {
				return nil, nil, serr
			}
		case a == "--budget", a == "--max-tokens", a == "--reserve", a == "--max-turns":
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
	case strings.HasPrefix(name, "--instructions"):
		f.instructions = v
	default:
		return fmt.Errorf("unknown flag %s", name)
	}
	return nil
}

// setToolFlag assigns one run-shaping flag (--tool, --tool-arg, --allow,
// --allow-file, --workdir, --agents, --hooks and the skills settings),
// resolving `--flag value` or `--flag=value`. Prefix order matters: --tool-arg
// and --allow-file must be matched before --tool and --allow.
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
	case strings.HasPrefix(name, "--agents"):
		if v == "" {
			return fmt.Errorf("flag --agents requires user, project or both")
		}
		f.agents = v
	case strings.HasPrefix(name, "--hooks"):
		return f.setHooks(v)
	case isSkillsFlag(name):
		return f.setSkillsFlag(name, v)
	default:
		f.workdir = v
	}
	return nil
}

// setLimit assigns one non-negative integer limit flag (--budget,
// --max-tokens); non-numeric or negative input is a usage error.
func setLimit(fl *flags, name, v string) error {
	if name == "--max-turns" {
		return setMaxTurns(fl, v)
	}
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

// setMaxTurns assigns --max-turns: a positive integer up to
// app.MaxTurnsLimit; anything else is a usage error.
func setMaxTurns(fl *flags, v string) error {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fmt.Errorf("flag --max-turns expects an integer from 1 to %d, got %q", app.MaxTurnsLimit, v)
	}
	if err := checkMaxTurns(n, true); err != nil {
		return err
	}
	fl.maxTurns = n
	return nil
}

// checkMaxTurns validates a turn limit: 0 is unset (the default) unless
// explicit, a negative or above-limit value is refused.
func checkMaxTurns(n int64, explicit bool) error {
	if n < 0 || n > app.MaxTurnsLimit || (explicit && n == 0) {
		return fmt.Errorf("max turns must be from 1 to %d, got %d", app.MaxTurnsLimit, n)
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
serve [--addr <host:port>] [--db path] [--config <path>] [--hooks user|project|both|none]

swarm run      <manifest> --execute [--agents user|project|both] [--provider <p> --model <m>] [--budget <n>]
               [--hooks user|project|both|none] [--max-turns <n>] [--max-tokens <n>] [--workdir <path>] [--db path]
               [--skills user|project|both|none] [--skills-path <abs dir>]...
               (a coordinator agent named by the manifest delegates the packets to role profiles; the engine
               runs the gates; exit 0 accepted, 8 not accepted or gate failed, 3/5/1 a failed coordinator,
               130 cancelled; without --execute: swarm run <manifest> --parent <id> --budget <n>;
               --skills and --skills-path load the skills the coordinator's and the roles' skills lists offer, as for
               session run; --skills-default is refused: the coordinator is a profile and names its own skills)
session new    --provider <p> --model <m> --task <text|@file> [--parent <id>] [--reserve <n>] [--db path]
session budget <id> [--db path]
session doctor <id> [--db path]
session message <id> --from <parent> --body <text|@file> [--db path]
session list   [--status <s>] [--db path]
session run    <id> [--budget <n>] [--max-tokens <n>] [--max-turns <n>] [--allow <name>]... [--allow-file <manifest>] [--workdir <path>]
               [--agents user|project|both] [--hooks user|project|both|none] [--db path]
               [--skills user|project|both|none] [--skills-path <abs dir>]... [--skills-default <list>]...
               (--hooks picks which lifecycle hook files apply, default user; none runs no hook;
               the project hook file is found under the project root, never --workdir;
               --skills picks which skills directories a run reads, default user; --skills-path adds an absolute
               directory and --skills-default names the skills the root session is offered; both also come from an
               explicit --config file, never the ambient flowstate.json; the project skills directory is found under
               the project root, never --workdir; --skills-path replaces skills_paths, --skills-default skills_default;
               --allow/--allow-file replace the config tool_allowlist; --workdir only sets where tools
               run and needs an allowlist from --allow, --allow-file or tool_allowlist; --agents loads agent
               profiles for the delegate tool, which must also be allowlisted: --allow delegate;
               project profiles come from --workdir, else the current directory;
               --max-turns 1..1000 bounds the tool loop, default 25, and a loop still asking for tools
               at the limit fails with max_turns, exit 5; it needs a tool loop like --allow does)
session events <id> [--follow] [--db path]
session close  <id> [--db path]
session compact <id> [--instructions <text>] [--db path]
session unlock <id> [--force] [--db path]
               (without --force: report the lease holder, delete nothing, exit 1 if a lease row exists;
               --force deletes a lease that cannot be judged, never one held by a live process here)
session cancel <id> [--db path]
session replay <id> [--db path]
receipt validate <file...>
session tool   <id> --tool <name> [--tool-arg <json>] [--allow <name>]... [--allow-file <manifest>] [--db path]
store info [--db path]
agents list [--agents user|project|both] [--workdir path]
agents validate <file...>
skills list [dir...]       (no dir: the --skills scope's directories, default user, and each --skills-path;
                           a dir replaces them; always JSON, --json is ignored)
skills validate [dir...]   (every diagnostic and the counts; exit 4 when a skill failed)

common flags: --db <path> (default ./flowstate.db), --config <path> (default ./flowstate.json), --json
`)
}

// setSwitch sets one flag that takes no value (--json, --follow, --force).
func (f *flags) setSwitch(name string) {
	switch name {
	case "--json":
		f.json = true
	case "--follow":
		f.follow = true
	case "--force":
		f.force = true
	}
}

// refuseSessionArgs reports a usage error for the positional arguments of a
// session subcommand (new and list take none, the others at most the id) and
// for --force given to anything but unlock.
func refuseSessionArgs(fl *flags, sub string, rest []string, stderr *os.File) bool {
	if sub == "new" || sub == "list" {
		if len(rest) > 0 {
			writeError(stderr, fmt.Errorf("%w: unexpected argument %q", errUsageCmd, rest[0]))
			return true
		}
	} else if len(rest) > 1 {
		writeError(stderr, fmt.Errorf("%w: unexpected argument %q", errUsageCmd, rest[1]))
		return true
	}
	if fl.force && sub != "unlock" {
		writeError(stderr, fmt.Errorf("%w: --force applies only to `session unlock`", errUsageCmd))
		return true
	}
	return false
}

// setHooks assigns --hooks. An empty value and any scope but user, project, both
// or none is a usage error; the default is applied by hooksScope, not here.
func (f *flags) setHooks(v string) error {
	if v == "" {
		return fmt.Errorf("flag --hooks requires user, project, both or none")
	}
	scope, perr := app.ParseHookScope(v)
	if perr != nil {
		return fmt.Errorf("%s", strings.TrimPrefix(perr.Error(), app.ErrUsage.Error()+": "))
	}
	f.hooks, f.hooksSet = scope, true
	return nil
}

// hooksScope is the invocation's effective --hooks scope: the CLI default, user,
// unless the flag was given. The default lives here, in the flag layer, so the
// app-level requests carry an already-parsed scope and their zero value stays "not set".
func hooksScope(fl *flags) app.HookScope {
	if fl.hooks == "" {
		return app.HookScopeUser
	}
	return fl.hooks
}

// acceptsHooks reports whether the command in args runs hooks and so takes
// --hooks: serve, session run and swarm run. Everything else refuses it.
func acceptsHooks(args []string) bool {
	switch args[0] {
	case "serve":
		return true
	case "session":
		return len(args) > 1 && args[1] == "run"
	case "swarm":
		return len(args) > 1 && args[1] == "run"
	}
	return false
}

// commandName is the command words of args for a usage message: at most the
// first two arguments that are not flags.
func commandName(args []string) string {
	words := []string{}
	for _, a := range args {
		if strings.HasPrefix(a, "-") || len(words) == 2 {
			break
		}
		words = append(words, a)
	}
	return strings.Join(words, " ")
}

// refuseInapplicableFlag writes the usage error for --hooks, or for a skills flag the command cannot take: the
// settings on a command that takes none of them, --skills-default on swarm run, which has no root session to offer.
func refuseInapplicableFlag(args []string, stderr *os.File) bool {
	if givesHooksFlag(args) && !acceptsHooks(args) {
		return refuseHooks(commandName(args), stderr)
	}
	if givesSkillsFlag(args) && !acceptsSkillsSettings(args) {
		return refuseSkillsSettings(commandName(args), stderr)
	}
	if givesSkillsDefaultFlag(args) && !acceptsSkillsDefault(args) {
		return refuseSkillsDefault(commandName(args), stderr)
	}
	return false
}

// refuseHooks writes the usage error for --hooks on a command that cannot run
// hooks and returns true.
func refuseHooks(command string, stderr *os.File) bool {
	writeError(stderr, fmt.Errorf("%w: --hooks does not apply to `%s`: it applies to `session run`, `serve` and "+
		"`swarm run --execute`", errUsageCmd, command))
	return true
}

// givesHooksFlag reports whether args carry --hooks, in either spelling.
func givesHooksFlag(args []string) bool {
	for _, a := range args {
		if a == "--hooks" || strings.HasPrefix(a, "--hooks=") {
			return true
		}
	}
	return false
}

// isSkillsFlag reports whether a is --skills, --skills-path or --skills-default, in either spelling.
func isSkillsFlag(a string) bool {
	name, _, _ := strings.Cut(a, "=")
	switch name {
	case "--skills", "--skills-path", "--skills-default":
		return true
	}
	return false
}

// setSkillsFlag assigns one skills flag its resolved value, checking each entry rule as it is added.
func (f *flags) setSkillsFlag(a, v string) error {
	name, _, _ := strings.Cut(a, "=")
	switch name {
	case "--skills":
		return f.setSkills(v)
	case "--skills-path":
		next := append(append([]string{}, f.skillsPaths...), v)
		if perr := checkSkillsPaths("--skills-path", next); perr != nil {
			return perr
		}
		f.skillsPaths = next
	default:
		next := append(append([]string{}, f.skillsDefault...), strings.Split(v, ",")...)
		if nerr := checkSkillsDefault("--skills-default", next); nerr != nil {
			return nerr
		}
		f.skillsDefault = next
	}
	return nil
}

// setSkills assigns --skills; an empty value or an unknown scope is a usage error.
func (f *flags) setSkills(v string) error {
	if v == "" {
		return fmt.Errorf("flag --skills requires user, project, both or none")
	}
	scope, perr := app.ParseSkillsScope(v)
	if perr != nil {
		return fmt.Errorf("%s", strings.TrimPrefix(perr.Error(), app.ErrUsage.Error()+": "))
	}
	f.skills, f.skillsSet = scope, true
	return nil
}

// skillsScope is the effective --skills scope: user unless the flag was given.
func skillsScope(fl *flags) app.SkillsScope {
	if fl.skills == "" {
		return app.SkillsScopeUser
	}
	return fl.skills
}

// acceptsSkillsSettings reports whether the command in args takes --skills and --skills-path: skills list,
// skills validate, session run and swarm run (whose --execute runs the coordinator over the scope; without
// --execute the flags are refused as execute-only). Everything else refuses them; serve joins when a later packet
// adds its flags.
func acceptsSkillsSettings(args []string) bool {
	switch args[0] {
	case "skills":
		return len(args) > 1 && (args[1] == "list" || args[1] == "validate")
	case "session":
		return len(args) > 1 && args[1] == "run"
	case "swarm":
		return len(args) > 1 && args[1] == "run"
	}
	return false
}

// refuseSkillsSettings writes the usage error for the skills settings flags on
// a command that cannot take them and returns true.
func refuseSkillsSettings(command string, stderr *os.File) bool {
	writeError(stderr, fmt.Errorf("%w: the skills settings do not apply to `%s`: --skills and --skills-path apply to "+
		"`skills list`, `skills validate`, `session run` and `swarm run --execute`; --skills-default to `session run` "+
		"(its shape is checked by `skills list` and `skills validate`)", errUsageCmd, command))
	return true
}

// givesSkillsFlag reports whether args carry a skills settings flag, in either
// spelling.
func givesSkillsFlag(args []string) bool {
	for _, a := range args {
		if isSkillsFlag(a) {
			return true
		}
	}
	return false
}

// acceptsSkillsDefault reports whether the command in args takes --skills-default: the commands that take the
// skills settings except swarm run, whose coordinator is a profile and lists its own `skills:` (ADR-013 D6), and
// which has no root session for skills_default to offer. skills list and skills validate take it for its shape only.
func acceptsSkillsDefault(args []string) bool {
	return acceptsSkillsSettings(args) && args[0] != "swarm"
}

// refuseSkillsDefault writes the usage error for --skills-default on a swarm run and returns true.
func refuseSkillsDefault(command string, stderr *os.File) bool {
	writeError(stderr, fmt.Errorf("%w: --skills-default does not apply to `%s`: the coordinator is a profile and lists its own "+
		"`skills:`; skills_default is for the root session only, and a swarm has none. It applies to `skills list`, "+
		"`skills validate` and `session run`", errUsageCmd, command))
	return true
}

// givesSkillsDefaultFlag reports whether args carry --skills-default, in either spelling.
func givesSkillsDefaultFlag(args []string) bool {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		if name == "--skills-default" {
			return true
		}
	}
	return false
}
