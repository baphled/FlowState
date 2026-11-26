package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// ExecuteSwarm runs one configured swarm with a coordinator agent: the
// coordinator session is given swarm.Brief(m) as its task and delegates each
// packet to its role profile; the engine then attributes the children to the
// packets, and only if the coordinator COMPLETED runs the manifest gates and
// the admission itself. The model's own claim about the gates is never read.
//
// Preconditions are checked before anything is created and fail with
// ErrUsage: the manifest, a coordinator and role profiles that are loaded, a
// role on every packet, `delegate` in the coordinator's tools, a streamer, a
// workdir, an admitter and a gate runner. The coordinator's delegate tool may
// spawn only the manifest's role profiles (the #445 allowlist, with the
// coordinator as delegator), so it cannot reach any other loaded profile. When
// the coordinator profile lists `delegates`, the allowlist is the intersection of
// those and the manifest's roles (least privilege); a packet role the
// intersection leaves out is refused up front, as no one could run that packet.
//
// Also refused: no packet (a swarm with no work would be accepted vacuously), no
// gate (the engine, not the model, decides acceptance), a packet whose role is
// the coordinator, and a gate runner whose Dir is not the Workdir. An empty
// `delegates` list imposes no further limit: the manifest's roles apply.
//
// A child is attributed to a packet when its first task starts with the
// marker `[packet <id>]` (swarm.PacketRef) naming a manifest packet, the
// child's profile is that packet's role AND the text after the marker contains
// the packet's task verbatim (whitespace runs collapsed on both sides); any
// other child is recorded without a packet. A packet is admitted only if a
// child attributed to it completed; the first completed one is named in the
// packet's Evidence. A packet with no child is recorded not run. A grandchild
// is not in the coordinator's log and is neither recorded nor attributed.
//
// Every outcome that the engine decided is a nil error with a Status; the
// store records only pass or fail, so the distinction (accepted, not
// accepted, gate failed, failed, cancelled) lives in the result for the
// caller to map onto exit codes: a gate_failed run and a cancel during the
// gates both store fail with the packets admitted, and no gate rows are kept
// (a later `swarm status` needs a richer record: a follow-up). An error means
// a precondition failed or persistence broke; the partial result is returned
// beside it. A cancelled run cancels the coordinator (and through it the
// tree), records every unfinished child as cancelled with one reason, and
// stores the run as failed. A deadline during the child phase or the gates is
// a failed run with reason `deadline`, not an error. A budget-exhausted
// coordinator is recorded failed (`budget_exhausted`) and stays paused. A
// panic in the coordinator loop or a child propagates once the child is
// settled and leaves the coordinator session active with no swarm run row,
// as `session run` does.
func ExecuteSwarm(ctx context.Context, st *Store, cfg ExecuteSwarmConfig) (ExecuteSwarmResult, error) {
	cfg.Hooks = emptyHookSet(cfg.Hooks)
	coord, err := checkExecuteSwarm(st, cfg)
	if err != nil {
		return ExecuteSwarmResult{}, err
	}
	m := cfg.Manifest
	sess, err := CreateSession(ctx, st, cfg.Provider, cfg.ModelID, cfg.Harness, cfg.ConfigSHA, swarm.Brief(m))
	if err != nil {
		return ExecuteSwarmResult{}, fmt.Errorf("app: execute swarm: create the coordinator session: %w", err)
	}
	res := ExecuteSwarmResult{CoordinatorID: sess.ID, Manifest: m}
	status, reason, cause, err := runSwarmCoordinator(ctx, st, sess, coord, cfg)
	if err != nil {
		return res, err
	}
	res.Status, res.Reason, res.Cause = status, reason, cause
	if res.Status == SwarmExecCancelled || res.Reason == reasonDeadline {
		err = settleCancelledChildren(ctx, st, &res)
	} else {
		res.Children, err = collectSwarmChildren(ctx, st, sess.ID, m)
	}
	if err != nil {
		return res, err
	}
	if res.Status != "" {
		err = recordFailedSwarm(ctx, st, &res)
		return res, err
	}
	return runSwarmEvidence(ctx, st, cfg, res)
}

// ExecuteSwarmFile is ExecuteSwarm for a manifest file, with the engine's own
// collaborators: the manifest is loaded from manifestPath (a failure is a usage
// error), packets are admitted by the hash-checking swarm.AdmissionLog, and the
// gates run in cfg.Workdir through swarm.NewGateRunnerIn. cfg.Manifest,
// cfg.Admitter and cfg.Gates are ignored. It is the seam cmd uses, as cmd does
// not import the swarm package. It also loads the hook set of cfg.HooksScope under cfg.ProjectRoot, unless
// cfg already carries one, before anything is written: a hook file that does not load fails the run as
// config_invalid. A swarm run writes no hooks.loaded (ADR-012, known gap 8).
func ExecuteSwarmFile(ctx context.Context, st *Store, manifestPath string, cfg ExecuteSwarmConfig) (ExecuteSwarmResult, error) {
	m, err := swarm.LoadManifest(manifestPath)
	if err != nil {
		return ExecuteSwarmResult{}, fmt.Errorf("%w: %v", ErrUsage, err)
	}
	hooks, _, err := loadRunHooks(cfg.Hooks, cfg.HooksScope, cfg.ProjectRoot)
	if err != nil {
		return ExecuteSwarmResult{}, err
	}
	cfg.Hooks = hooks
	cfg.Manifest, cfg.Admitter, cfg.Gates = m, swarm.NewAdmissionLog(), swarm.NewGateRunnerIn(cfg.Workdir)
	return ExecuteSwarm(ctx, st, cfg)
}

// checkExecuteSwarm checks every precondition, writing nothing, and returns the
// coordinator's profile. Each failure is a usage error naming the fix.
func checkExecuteSwarm(st *Store, cfg ExecuteSwarmConfig) (agent.Profile, error) {
	m := cfg.Manifest
	switch {
	case st == nil, cfg.Streamer == nil, cfg.Admitter == nil, cfg.Gates == nil:
		return agent.Profile{}, fmt.Errorf("%w: execute swarm: the store, streamer, admitter and gates are required", ErrUsage)
	case cfg.Workdir == "", cfg.Provider == "", cfg.ModelID == "":
		return agent.Profile{}, fmt.Errorf("%w: execute swarm: the workdir, provider and model are required", ErrUsage)
	}
	if err := swarm.ValidateManifest(m); err != nil {
		return agent.Profile{}, fmt.Errorf("%w: execute swarm: %w", ErrUsage, err)
	}
	if len(m.Packets) == 0 {
		return agent.Profile{}, fmt.Errorf("%w: execute swarm: add at least one packet", ErrUsage)
	}
	if len(m.Gates) == 0 {
		return agent.Profile{}, fmt.Errorf("%w: execute swarm: an executed swarm needs at least one gate: "+
			"the engine, not the model, decides acceptance", ErrUsage)
	}
	if cfg.Gates.Dir() == "" || filepath.Clean(cfg.Gates.Dir()) != filepath.Clean(cfg.Workdir) {
		return agent.Profile{}, fmt.Errorf("%w: execute swarm: gates must run in the swarm workdir: "+
			"build the runner with swarm.NewGateRunnerIn(workdir)", ErrUsage)
	}
	names := make([]string, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		names = append(names, p.Name)
	}
	if err := swarm.CheckExecutable(m, names); err != nil {
		return agent.Profile{}, fmt.Errorf("%w: execute swarm: %w", ErrUsage, err)
	}
	for _, p := range m.Packets {
		if p.Role == "" {
			return agent.Profile{}, fmt.Errorf("%w: execute swarm: packet %s has no role: add `role: <profile>`", ErrUsage, p.ID)
		}
		if p.Role == m.Coordinator {
			return agent.Profile{}, fmt.Errorf("%w: execute swarm: packet %s has the coordinator %q as its role: "+
				"use another profile", ErrUsage, p.ID, p.Role)
		}
	}
	coord := cfg.Profiles[slices.Index(names, m.Coordinator)]
	if !slices.Contains(coord.Tools, agent.DelegateTool) {
		return agent.Profile{}, fmt.Errorf("%w: execute swarm: coordinator profile %q does not list `delegate` in its tools: add it",
			ErrUsage, coord.Name)
	}
	return coord, checkCoordinatorReach(m, coord)
}

// swarmAllowlist is the profiles the coordinator's delegate tool may spawn: the
// manifest's roles, narrowed to the coordinator profile's `delegates` when it
// lists any.
func swarmAllowlist(m swarm.Manifest, coord agent.Profile) []string {
	roles := manifestRoles(m)
	if len(coord.Delegates) == 0 {
		return roles
	}
	return slices.DeleteFunc(roles, func(r string) bool { return !slices.Contains(coord.Delegates, r) })
}

// checkCoordinatorReach refuses a manifest with a packet role the coordinator
// cannot delegate to: a packet nobody can run can never be admitted.
func checkCoordinatorReach(m swarm.Manifest, coord agent.Profile) error {
	allowed := swarmAllowlist(m, coord)
	for _, p := range m.Packets {
		if !slices.Contains(allowed, p.Role) {
			return fmt.Errorf("%w: execute swarm: packet %s has the role %q, which coordinator %q cannot delegate to: "+
				"add it to the coordinator's `delegates`", ErrUsage, p.ID, p.Role, coord.Name)
		}
	}
	return nil
}

// manifestRoles lists the distinct role profiles the manifest names.
func manifestRoles(m swarm.Manifest) []string {
	var roles []string
	for _, p := range m.Packets {
		if p.Role != "" && !slices.Contains(roles, p.Role) {
			roles = append(roles, p.Role)
		}
	}
	return roles
}

// coordinatorLoopConfig is the coordinator's loop: the profile's tools as the allowlist, its body as
// the system prompt, and the run's root hook set (ADR-012 D22).
func coordinatorLoopConfig(st *Store, sess *store.Session, coord agent.Profile, cfg ExecuteSwarmConfig,
	registry *tool.Registry, maxTurns int64) ExecLoopConfig {
	return ExecLoopConfig{
		Store: st, Session: sess, Streamer: cfg.Streamer, Registry: registry, Allowlist: coord.Tools,
		MaxTurns: maxTurns, MaxTokens: cfg.MaxTokens,
		SystemPrompt: coord.Body, Compaction: cfg.Compaction, ProfileWindow: coord.ContextWindow,
		Hooks: emptyHookSet(cfg.Hooks), Workdir: cfg.Workdir, Agent: coord.Name,
	}
}

// coordinatorToolConfig is the coordinator's delegate tool; it hands the run's root hook set to
// every packet child (ADR-012 D22).
func coordinatorToolConfig(st *Store, sess *store.Session, coord agent.Profile, cfg ExecuteSwarmConfig,
	pool *BudgetPool) DelegateToolConfig {
	return DelegateToolConfig{
		Store: st, Parent: sess, Streamer: cfg.Streamer, Pool: pool, Workdir: cfg.Workdir, Profiles: cfg.Profiles,
		DelegatorProfile: coord.Name, AllowedProfiles: swarmAllowlist(cfg.Manifest, coord), Compaction: cfg.Compaction,
		Hooks: emptyHookSet(cfg.Hooks),
	}
}

// runSwarmCoordinator composes the coordinator's loop as RunSessionExecLoop does
// (the profile's tools as the allowlist, its body as the system prompt) and runs
// it. Its delegate tool is bound to the coordinator profile and restricted to the
// manifest's roles, narrowed by the profile's `delegates` (swarmAllowlist). It returns an empty status when the coordinator completed;
// otherwise failed or cancelled with the reason and the error that stopped the
// loop (cause). The error is persistence only.
func runSwarmCoordinator(ctx context.Context, st *Store, sess *store.Session, coord agent.Profile,
	cfg ExecuteSwarmConfig) (status, reason string, cause, err error) {
	registry, err := NewSessionToolRegistry(cfg.Workdir)
	if err != nil {
		return "", "", nil, err
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = int64(coord.MaxTurns)
	}
	loop := coordinatorLoopConfig(st, sess, coord, cfg, registry, maxTurns)
	if err := bindRunBudget(ctx, st, sess, cfg.Budget, &loop); err != nil {
		return "", "", nil, err
	}
	err = registry.Register(NewDelegateTool(coordinatorToolConfig(st, sess, coord, cfg, loop.Pool)))
	if err != nil {
		return "", "", nil, err
	}
	// The coordinator's handle outlives the loop and the post-loop writers (ADR-011 rule 8).
	lease, err := acquireCoordinatorLease(ctx, st, sess.ID)
	if err != nil {
		return "", "", nil, err
	}
	defer func() { _ = lease.Release(context.WithoutCancel(ctx)) }()
	loop.Lease = lease
	res, runErr := RunExecLoop(ctx, loop)
	if runErr != nil {
		stop := finishExecLoopState(ctx, st, sess, runErr)
		status, reason, err = classifyCoordinatorStop(ctx, stop)
		return status, reason, stop, err
	}
	return "", "", nil, completeExecLoopRun(ctx, st, sess.ID, res.FinishReason)
}

// acquireCoordinatorLease takes the exec lease of the coordinator's session under a fresh "run:<id>"
// owner. A refusal (a live or unjudgeable holder) is returned as an error with nothing written: a
// process that was refused must not write the holder's log.
func acquireCoordinatorLease(ctx context.Context, st *Store, sessionID string) (*store.RunLease, error) {
	id, err := session.NewID()
	if err != nil {
		return nil, fmt.Errorf("generate lease owner: %w", err)
	}
	lease, err := st.AcquireRunLease(ctx, "exec:"+sessionID, runLeasePrefix+id)
	if err != nil {
		return nil, fmt.Errorf("app: execute swarm: coordinator lease: %w", err)
	}
	return lease, nil
}

// classifyCoordinatorStop maps the error that ended the coordinator onto the
// swarm status and reason; it is never a persistence error.
func classifyCoordinatorStop(ctx context.Context, err error) (status, reason string, _ error) {
	switch {
	case errors.Is(err, ErrSessionCancelled):
		return interruptedSwarm(ctx)
	case errors.Is(err, ErrBudgetExceeded):
		return SwarmExecFailed, "budget_exhausted", nil
	case errors.Is(err, ErrMaxTurns):
		return SwarmExecFailed, CodeMaxTurns, nil
	case errors.Is(err, ErrOutputTruncated):
		return SwarmExecFailed, CodeOutputTruncated, nil
	case errors.Is(err, ErrHookStop):
		return SwarmExecFailed, CodeHookStop, nil
	}
	return SwarmExecFailed, clipRunes(err.Error(), swarmReasonMaxChars), nil
}

// reasonDeadline is the reason of a run whose context deadline passed.
const reasonDeadline = "deadline"

// interruptedSwarm maps a done context onto the swarm status: a passed deadline
// is a failed run with reason deadline, any other cancellation is cancelled.
func interruptedSwarm(ctx context.Context) (status, reason string, _ error) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return SwarmExecFailed, reasonDeadline, nil
	}
	return SwarmExecCancelled, CancelReason(ctx), nil
}

// swarmReasonMaxChars bounds a failure text kept as a run's reason.
const swarmReasonMaxChars = 300

// collectSwarmChildren lists the coordinator's delegated children from its own
// log (the delegation.started receipts, first start first) and attributes each.
func collectSwarmChildren(ctx context.Context, st *Store, coordID string, m swarm.Manifest) ([]ExecutedChild, error) {
	events, err := st.EventsAfter(context.WithoutCancel(ctx), coordID, 0)
	if err != nil {
		return nil, fmt.Errorf("app: execute swarm: read the coordinator log: %w", err)
	}
	var children []ExecutedChild
	seen := map[string]bool{}
	for _, e := range events {
		if e.Type != TypeDelegationStarted {
			continue
		}
		var p struct {
			ChildID string              `json:"child_id"`
			Profile *DelegateProfileRef `json:"profile"`
		}
		if json.Unmarshal([]byte(e.PayloadJSON), &p) != nil || p.ChildID == "" || p.Profile == nil || seen[p.ChildID] {
			continue
		}
		seen[p.ChildID] = true
		child, cerr := describeSwarmChild(ctx, st, p.ChildID, p.Profile.Name, m)
		if cerr != nil {
			return nil, cerr
		}
		children = append(children, child)
	}
	return children, nil
}

// describeSwarmChild reads one child's status, turns and first task and decides
// its packet: the marker must name a manifest packet whose role is the child's
// profile, else the child stays unattributed.
func describeSwarmChild(ctx context.Context, st *Store, id, profile string, m swarm.Manifest) (ExecutedChild, error) {
	dctx := context.WithoutCancel(ctx)
	cur, err := st.GetSession(dctx, id)
	if err != nil {
		return ExecutedChild{}, fmt.Errorf("app: execute swarm: load child %s: %w", id, err)
	}
	events, err := st.Replay(dctx, id)
	if err != nil {
		return ExecutedChild{}, fmt.Errorf("app: execute swarm: replay child %s: %w", id, err)
	}
	child := ExecutedChild{ID: id, Profile: profile, Status: cur.Status}
	child.Turns, _ = countTurns(events)
	child.Reason = terminalErrorReason(events)
	for _, e := range events {
		if e.Type != session.TypeTurnStart {
			continue
		}
		child.PacketID = attributedPacket(payloadFieldText(e.PayloadJSON, "task"), profile, m)
		break
	}
	return child, nil
}

// terminalErrorReason is the reason of the last terminal provider.error of a log, or "".
func terminalErrorReason(events []store.RecordedEvent) string {
	reason := ""
	for _, e := range events {
		if e.Type == TypeProviderError && payloadFieldRaw(e.PayloadJSON, "terminal") == "true" {
			reason = payloadFieldText(e.PayloadJSON, "reason")
		}
	}
	return reason
}

// attributedPacket is the engine's one attribution rule, applied to a child's
// first task: the packet id the marker names, if the manifest has that packet
// and the child ran under the packet's role and the text after the marker holds
// the packet's task verbatim (whitespace runs collapsed); otherwise empty.
func attributedPacket(task, profile string, m swarm.Manifest) string {
	id, ok := swarm.PacketRef(task)
	if !ok {
		return ""
	}
	_, rest, _ := strings.Cut(task, "]")
	for _, p := range m.Packets {
		if p.ID == id && p.Role == profile && strings.Contains(squashSpace(rest), squashSpace(p.Task)) {
			return id
		}
	}
	return ""
}

// squashSpace collapses every run of whitespace to one space.
func squashSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// storedSwarmChildren maps the children onto the run record's child rows.
func storedSwarmChildren(children []ExecutedChild) []store.SwarmRunChild {
	out := make([]store.SwarmRunChild, 0, len(children))
	for _, c := range children {
		out = append(out, store.SwarmRunChild{PacketID: c.PacketID, SessionID: c.ID})
	}
	return out
}

// settleCancelledChildren records every unfinished child as cancelled with the
// run's one reason (final event before status, reservation settled, on a
// detached context) and refreshes res.Children with the settled states.
func settleCancelledChildren(ctx context.Context, st *Store, res *ExecuteSwarmResult) error {
	children, err := collectSwarmChildren(ctx, st, res.CoordinatorID, res.Manifest)
	if err != nil {
		return err
	}
	var owed []SwarmChild
	for _, c := range children {
		owed = append(owed, SwarmChild{PacketID: c.PacketID, Session: store.Session{ID: c.ID}})
	}
	cerr := cancelSwarmChildren(context.WithoutCancel(ctx), st, owed, res.Reason)
	settled, err := collectSwarmChildren(ctx, st, res.CoordinatorID, res.Manifest)
	res.Children = settled
	return errors.Join(cerr, err)
}

// recordFailedSwarm stores a run whose coordinator did not complete: outcome
// fail, every packet not admitted, the children named, no gate evaluated.
func recordFailedSwarm(ctx context.Context, st *Store, res *ExecuteSwarmResult) error {
	sha, err := swarmManifestSHA(res.Manifest)
	if err != nil {
		return err
	}
	byPacket := attributedChildren(res.Children)
	run := store.SwarmRun{
		Name: res.Manifest.Name, ManifestSHA256: sha, Outcome: store.SwarmRunOutcomeFail,
		Children: storedSwarmChildren(res.Children),
	}
	for _, p := range res.Manifest.Packets {
		why := "coordinator " + res.Status + ": " + res.Reason
		if _, ran := byPacket[p.ID]; !ran {
			why = "not run: " + why
		}
		run.Packets = append(run.Packets, store.SwarmRunPacket{PacketID: p.ID, Reason: why})
		res.Packets = append(res.Packets, ExecutedPacket{ID: p.ID, Reason: why})
	}
	rec, err := st.RecordSwarmRun(context.WithoutCancel(ctx), run)
	if err != nil {
		return fmt.Errorf("app: execute swarm: record the failed run: %w", err)
	}
	res.RunID = rec.ID
	return nil
}

// attributedChildren maps each packet to the child that stands for it: a
// completed child wins over any other (the first completed one), else the latest.
func attributedChildren(children []ExecutedChild) map[string]ExecutedChild {
	out := map[string]ExecutedChild{}
	for _, c := range children {
		if c.PacketID == "" {
			continue
		}
		if prev, ok := out[c.PacketID]; ok && prev.Status == "completed" {
			continue
		}
		out[c.PacketID] = c
	}
	return out
}

// Admit refuses a packet no completed child stands for, then asks the wrapped
// admitter; every decision is kept for the result.
func (a *packetAdmitter) Admit(p swarm.Packet, body []byte) error {
	err := a.decide(p, body)
	d := ExecutedPacket{ID: p.ID, Admitted: err == nil}
	if err != nil {
		d.Reason = err.Error()
	} else {
		d.Evidence = "admitted by child " + a.byPacket[p.ID].ID
	}
	a.decisions = append(a.decisions, d)
	return err
}

// decide is the packet's admission decision.
func (a *packetAdmitter) decide(p swarm.Packet, body []byte) error {
	child, ran := a.byPacket[p.ID]
	switch {
	case !ran:
		return fmt.Errorf("not run: no delegated child carried the marker [packet %s] and the task text under role %s",
			p.ID, p.Role)
	case child.Status != "completed" && child.Reason != "":
		return fmt.Errorf("child %s is %s, not completed (reason: %s)", child.ID, child.Status, child.Reason)
	case child.Status != "completed":
		return fmt.Errorf("child %s is %s, not completed", child.ID, child.Status)
	}
	return a.inner.Admit(p, body)
}

// RecordSwarmRun stores the run and keeps the id the store assigned.
func (s *idCaptureSink) RecordSwarmRun(ctx context.Context, run store.SwarmRun) (store.SwarmRun, error) {
	rec, err := s.inner.RecordSwarmRun(ctx, run)
	s.id = rec.ID
	return rec, err
}

// runSwarmEvidence is the engine's half after a completed coordinator: the
// existing admission-and-gate run over the packets, recorded durably with the
// children, then the status the decision implies.
func runSwarmEvidence(ctx context.Context, st *Store, cfg ExecuteSwarmConfig, res ExecuteSwarmResult) (ExecuteSwarmResult, error) {
	m := cfg.Manifest
	sha, err := swarmManifestSHA(m)
	if err != nil {
		return res, err
	}
	adm := &packetAdmitter{inner: cfg.Admitter, byPacket: attributedChildren(res.Children)}
	sink := &idCaptureSink{inner: st}
	receipts := swarm.NewDurableReceipts(sink, sha).WithChildren(storedSwarmChildren(res.Children))
	run, rerr := swarm.Run(ctx, m, adm, cfg.Gates, nil, receipts)
	res.RunID, res.Packets = sink.id, adm.decisions
	for _, o := range run.Gates.Outcomes {
		res.Gates = append(res.Gates, ExecutedGate{Name: o.Gate.Name, Passed: o.Passed, Interrupted: o.Interrupted})
	}
	if errors.Is(rerr, context.Canceled) || errors.Is(rerr, context.DeadlineExceeded) {
		res.Status, res.Reason, _ = interruptedSwarm(ctx)
		err = settleCancelledChildren(ctx, st, &res)
		return res, err
	}
	if rerr != nil {
		return res, fmt.Errorf("app: execute swarm: %w", rerr)
	}
	res.Status = swarmDecisionStatus(m, run)
	return res, nil
}

// swarmDecisionStatus maps the gate and admission decision onto a status: a
// gate that did not pass outranks a rejected packet (an executed swarm always
// has a gate: the precondition refuses none).
func swarmDecisionStatus(m swarm.Manifest, run swarm.RunResult) string {
	switch {
	case run.Gates.Accepted:
		return SwarmExecAccepted
	case len(run.Gates.Outcomes) < len(m.Gates) || len(run.Gates.FailedGateNames()) > 0:
		return SwarmExecGateFailed
	}
	return SwarmExecNotAccepted
}

// RequireDirectory reports an error unless path names an existing directory (a
// symbolic link to one counts). It is the check cmd makes on --workdir before
// anything is created.
func RequireDirectory(path string) error {
	if !isDirectory(path) {
		return fmt.Errorf("%w: %q", ErrUsage, path)
	}
	return nil
}

// RequireDatabaseOutside reports an ErrUsage error when the database file or
// one of its WAL sidecars lies inside workdir: a role with a write tool could
// overwrite the engine's own database there. Both paths are made absolute and
// their symbolic links resolved; the database need not exist yet, so the
// nearest existing ancestor is resolved. It runs before the store is opened.
func RequireDatabaseOutside(db, workdir string) error {
	root := resolvePath(workdir)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		rel, err := filepath.Rel(root, resolvePath(db+suffix))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%w: --db %q is inside --workdir %q: the swarm's tools can write there; give --db a path outside it",
				ErrUsage, db+suffix, workdir)
		}
	}
	return nil
}

// resolvePath is the absolute path with symbolic links resolved as far as the
// path exists; the missing tail is appended unresolved. A dangling link is
// followed to where SQLite would create its target (at most 16 links deep).
func resolvePath(path string) string { return resolveDepth(path, 16) }

func resolveDepth(path string, depth int) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	tail := ""
	for cur := abs; ; cur = filepath.Dir(cur) {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, tail)
		}
		if target, err := os.Readlink(cur); err == nil && depth > 0 {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(cur), target)
			}
			return filepath.Join(resolveDepth(target, depth-1), tail)
		}
		if filepath.Dir(cur) == cur {
			return abs
		}
		tail = filepath.Join(filepath.Base(cur), tail)
	}
}
