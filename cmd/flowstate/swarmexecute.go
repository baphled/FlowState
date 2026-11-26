package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/baphled/flowstate/internal/app"
)

// swarmExecutor is the CLI's seam for an executed swarm; specs swap it to observe
// the config, production runs app.ExecuteSwarmFile.
var swarmExecutor = app.ExecuteSwarmFile

// valueFlags are the flags that take the next argument as their value.
var valueFlags = []string{"--db", "--status", "--provider", "--model", "--task", "--parent", "--config", "--manifest",
	"--from", "--body", "--tool", "--allow", "--allow-file", "--workdir", "--agents", "--budget", "--max-tokens",
	"--reserve", "--max-turns", "--hooks"}

// withoutExecute drops every --execute from args and reports whether one was
// given; the flag takes no value. An --execute straight after a flag that takes
// a value would be that flag's value, so it is refused naming the flag.
func withoutExecute(args []string) ([]string, bool, error) {
	out, found := make([]string, 0, len(args)), false
	for i, a := range args {
		if a != "--execute" {
			out = append(out, a)
			continue
		}
		if i > 0 && slices.Contains(valueFlags, args[i-1]) {
			return nil, false, fmt.Errorf("%w: %s needs a value, found --execute", app.ErrUsage, args[i-1])
		}
		found = true
	}
	return out, found, nil
}

// refuseExecuteOnlyFlags is the usage check of a receipts-only `swarm run`: the
// flags that only configure an executed swarm are refused instead of ignored.
// The first given flag in this fixed order is the one named.
func refuseExecuteOnlyFlags(fl *flags, stderr *os.File) (int, bool) {
	for _, f := range []struct {
		name  string
		given bool
	}{
		{"--agents", fl.agents != ""}, {"--provider", fl.provider != ""}, {"--model", fl.model != ""},
		{"--hooks", fl.hooksSet}, {"--workdir", fl.workdir != ""}, {"--max-turns", fl.maxTurns != 0}, {"--max-tokens", fl.maxTokens != 0},
	} {
		if f.given {
			writeError(stderr, fmt.Errorf("%w: %s only applies to `swarm run --execute`: add --execute", app.ErrUsage, f.name))
			return exitUsage, true
		}
	}
	return exitOK, false
}

// swarmRunExecute implements `swarm run <manifest> --execute`: the manifest's
// coordinator profile runs the swarm through app.ExecuteSwarmFile, then one JSON
// result is printed (schema swarm-run-execute). The provider and model resolve
// as `session new` does (flags, then config defaults); --agents picks the
// profile scope and --workdir (default: the current directory) is where the
// coordinator's tools, the children and the engine's gates work. A coordinator
// session is created by the engine, so --parent is refused.
//
// Exit codes reuse the existing table: 0 accepted; 8 not accepted (a gate
// failed or a packet was not admitted, as in the receipts-only run); a failed
// coordinator keeps its own class (3 provider error, 5 budget, max_turns, output_truncated or hook_stop
// (a turn.after hook stop, ADR-012 D7), 1 anything else, including a passed deadline); 130 cancelled by SIGINT, SIGTERM
// or SIGHUP; 2 usage and 4 config_invalid before anything is created.
func swarmRunExecute(ctx context.Context, fl *flags, rest []string, stdout, stderr *os.File) int {
	manifestPath := fl.manifestPath
	if manifestPath == "" && len(rest) == 1 {
		manifestPath = rest[0]
	}
	if manifestPath == "" || fl.parent != "" {
		writeError(stderr, fmt.Errorf("%w: swarm run --execute takes one manifest path and no --parent", app.ErrUsage))
		return exitUsage
	}
	if code, done := applySessionConfig(fl, stderr); done {
		return code
	}
	applyRunDefaults(fl)
	cfg, code, done := executeConfig(fl, manifestPath, stderr)
	if done {
		return code
	}
	st, err := openStore(fl.db)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: cannot open database %q: %v", app.ErrConfigInvalid, fl.db, err))
		return exitConfigInvalid
	}
	defer st.Close()
	res, err := swarmExecutor(ctx, st, manifestPath, cfg)
	if err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	if err := writeJSON(stdout, swarmExecuteJSON(res)); err != nil {
		writeError(stderr, err)
		return exitCodeFor(err)
	}
	return swarmExecuteExit(res, fl, stderr)
}

// executeConfig resolves everything an executed swarm needs before a store is
// opened: the database must lie outside the workdir, the manifest must name a coordinator, the profiles load for the
// scope, and the provider resolves to a streamer.
func executeConfig(fl *flags, manifestPath string, stderr *os.File) (app.ExecuteSwarmConfig, int, bool) {
	provider, model := effectiveProvider(fl), effectiveModel(fl)
	if provider == "" || model == "" {
		writeError(stderr, fmt.Errorf("%w: --provider and --model are required", app.ErrUsage))
		return app.ExecuteSwarmConfig{}, exitUsage, true
	}
	m, err := app.ValidateSwarmManifest(manifestPath)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: %v", app.ErrUsage, err))
		return app.ExecuteSwarmConfig{}, exitUsage, true
	}
	if m.Coordinator == "" {
		writeError(stderr, fmt.Errorf("%w: manifest %s has no coordinator: add `coordinator: <profile>` to run it with --execute",
			app.ErrUsage, m.Name))
		return app.ExecuteSwarmConfig{}, exitUsage, true
	}
	workdir, err := filepath.Abs(fl.workdir)
	if err != nil {
		writeError(stderr, fmt.Errorf("%w: --workdir: %v", app.ErrUsage, err))
		return app.ExecuteSwarmConfig{}, exitUsage, true
	}
	if err := app.RequireDirectory(workdir); err != nil {
		writeError(stderr, fmt.Errorf("%w: --workdir %q is not a directory", app.ErrUsage, workdir))
		return app.ExecuteSwarmConfig{}, exitUsage, true
	}
	if err := app.RequireDatabaseOutside(fl.db, workdir); err != nil {
		writeError(stderr, err)
		return app.ExecuteSwarmConfig{}, exitUsage, true
	}
	profiles, err := app.LoadProfiles(fl.agents, workdir)
	if err != nil {
		writeError(stderr, err)
		return app.ExecuteSwarmConfig{}, exitCodeFor(err), true
	}
	sha, err := compositionSHA256(provider, model, fl.db, fl.configSource, effectiveAllowlist(fl))
	if err != nil {
		writeError(stderr, err)
		return app.ExecuteSwarmConfig{}, exitCodeFor(err), true
	}
	p, err := providerResolver(provider)
	s, ok := p.(app.Streamer)
	if err == nil && !ok {
		err = fmt.Errorf("provider %s does not support streaming turns", provider)
	}
	if err != nil {
		writeError(stderr, providerFailure(err))
		return app.ExecuteSwarmConfig{}, exitProviderError, true
	}
	return app.ExecuteSwarmConfig{
		Profiles: profiles, Streamer: s, Provider: provider, ModelID: model, Harness: app.DefaultHarness,
		ConfigSHA: sha, Workdir: workdir, MaxTurns: fl.maxTurns, MaxTokens: fl.maxTokens, Budget: fl.budget,
		Compaction: fl.config.Compaction, HooksScope: hooksScope(fl),
	}, exitOK, false
}

// swarmExecuteJSON renders the result as the swarm-run-execute object; every
// list is an array, empty when nothing was recorded.
func swarmExecuteJSON(res app.ExecuteSwarmResult) map[string]any {
	children := make([]map[string]any, 0, len(res.Children))
	for _, c := range res.Children {
		children = append(children, map[string]any{
			"id": c.ID, "profile": c.Profile, "packet": c.PacketID, "status": c.Status, "turns": c.Turns,
		})
	}
	gates := make([]map[string]any, 0, len(res.Gates))
	for _, g := range res.Gates {
		gates = append(gates, map[string]any{"name": g.Name, "passed": g.Passed, "interrupted": g.Interrupted})
	}
	packets := make([]map[string]any, 0, len(res.Packets))
	for _, p := range res.Packets {
		packets = append(packets, map[string]any{
			"id": p.ID, "admitted": p.Admitted, "reason": p.Reason, "evidence": p.Evidence,
		})
	}
	return map[string]any{
		"manifest": res.Manifest.Name, "run_id": res.RunID, "status": res.Status, "reason": res.Reason,
		"coordinator_session": res.CoordinatorID, "children": children, "gates": gates, "packets": packets,
	}
}

// swarmExecuteExit maps the engine's decision onto the CLI exit code and writes
// the matching error object on stderr for every status but accepted.
func swarmExecuteExit(res app.ExecuteSwarmResult, fl *flags, stderr *os.File) int {
	switch {
	case res.Status == app.SwarmExecAccepted:
		return exitOK
	case res.Status == app.SwarmExecCancelled:
		return reportInterrupt(errors.Join(app.ErrSessionCancelled, res.Cause), stderr)
	case res.Reason == "deadline":
		writeError(stderr, fmt.Errorf("swarm %s failed: the deadline passed", res.Manifest.Name))
		return exitUnknownSession
	case res.Status == app.SwarmExecFailed && res.Cause != nil:
		return finishRun(res.Cause, nil, fl, stderr)
	case res.Status == app.SwarmExecFailed:
		writeError(stderr, fmt.Errorf("swarm %s failed: %s", res.Manifest.Name, res.Reason))
		return exitUnknownSession
	}
	failed, rejected := []string{}, []string{}
	for _, g := range res.Gates {
		if !g.Passed {
			failed = append(failed, g.Name)
		}
	}
	for _, p := range res.Packets {
		if !p.Admitted {
			rejected = append(rejected, p.ID)
		}
	}
	err := fmt.Errorf("%w: swarm %s is %s: failed gates %v, rejected packets %v",
		app.ErrSwarmNotAccepted, res.Manifest.Name, res.Status, failed, rejected)
	writeError(stderr, err)
	return exitCodeFor(err)
}
