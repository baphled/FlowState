package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/swarm"
)

// RunSwarm executes one configured swarm on behalf of a parent
// session: every manifest packet becomes a delegated child session in
// RunOrder (prerequisites first), each child receives budgetPerChild
// from the parent's declared pool, and each child's packet attribution
// lands as a steering message from the parent. After the children
// exist, the admission-and-gate run drives with durable receipts so
// the complete swarm run persists with its outcome.
//
// RunSwarm does NOT execute provider turns: each child is created
// active with its packet task and is driven by an ordinary session
// run (S2 execution semantics).
//
// Budget policy: budgetPerChild is the even share the caller split
// from the parent pool (S2 policy; per-packet manifest budgets are
// M2+). A non-positive share creates children without reservations.
//
// Failure semantics (fail-closed, S2): an invalid manifest aborts
// before any creation; any child-creation, reservation or steering
// failure aborts the run with the partial result: the children already
// created are cancelled as swarm_aborted, their reservations settled,
// and a failed run naming them is stored (see abortSwarmCreation).
// Budget failures surface as ErrUsage through the ReserveChildBudget
// seam. A run cancelled after children exist records each unfinished
// child as cancelled (see cancelSwarmChildren) and names them on its
// stored run. When a gate fails or admission rejects, the complete child set stays and
// the error reports the swarm as not accepted.
func RunSwarm(ctx context.Context, st *Store, parent *store.Session, m swarm.Manifest,
	budgetPerChild int64, admitter swarm.RunAdmitter, gates *swarm.GateRunner) (SwarmExecResult, error) {
	if st == nil || parent == nil {
		return SwarmExecResult{}, fmt.Errorf("%w: run swarm: store and parent session are required", ErrUsage)
	}
	if err := swarm.ValidateManifest(m); err != nil {
		return SwarmExecResult{}, fmt.Errorf("app: run swarm: %w", err)
	}
	if admitter == nil || gates == nil {
		return SwarmExecResult{}, fmt.Errorf("%w: run swarm: admitter and gates are required", ErrUsage)
	}
	children, reservations, err := swarmChildren(ctx, st, parent, m, budgetPerChild)
	res := SwarmExecResult{Children: children, Reservations: reservations, Manifest: m}
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			cerr := recordCancelledCreation(ctx, st, m, res, admitter, gates)
			return res, settleCancelledSwarm(ctx, st, res, errors.Join(mapSwarmRunError(ctx.Err()), cerr))
		}
		return res, abortSwarmCreation(ctx, st, m, res, admitter, gates, err)
	}
	run, err := swarmRunDurable(ctx, st, m, res, admitter, gates)
	res.Admitted, res.Gates = run.Admitted, run.Gates
	if err != nil {
		return res, settleCancelledSwarm(ctx, st, res, err)
	}
	if !run.Gates.Accepted {
		return res, swarmNotAccepted(m, run)
	}
	return res, nil
}

// swarmChildren creates one active child per packet in RunOrder,
// reserving the budget share and attributing the packet after each
// creation; any failure aborts with the children created so far.
func swarmChildren(ctx context.Context, st *Store, parent *store.Session,
	m swarm.Manifest, budgetPerChild int64) ([]SwarmChild, []SwarmReservation, error) {
	var children []SwarmChild
	var reservations []SwarmReservation
	for _, p := range swarm.RunOrder(m) {
		child, err := CreateChildSession(ctx, st, parent.ID, parent.Provider,
			parent.ModelID, parent.Harness, parent.ConfigSHA, p.Task)
		if err != nil {
			return children, reservations, fmt.Errorf("app: run swarm: create child for %s: %w", p.ID, err)
		}
		children = append(children, SwarmChild{PacketID: p.ID, Session: *child})
		res, rerr := reserveSwarmChild(ctx, st, parent.ID, child.ID, p.ID, budgetPerChild)
		if rerr != nil {
			return children, reservations, rerr
		}
		if res != nil {
			reservations = append(reservations, *res)
		}
		if aerr := attributeSwarmChild(ctx, st, parent.ID, child.ID, p.ID); aerr != nil {
			return children, reservations, aerr
		}
	}
	return children, reservations, nil
}

// reserveSwarmChild reserves the child's even budget share against the
// parent's declared pool; a non-positive share skips the reservation.
func reserveSwarmChild(ctx context.Context, st *Store, parentID, childID, packetID string,
	budgetPerChild int64) (*SwarmReservation, error) {
	if budgetPerChild <= 0 {
		return nil, nil
	}
	if err := ReserveChildBudget(ctx, st, parentID, childID, budgetPerChild); err != nil {
		return nil, fmt.Errorf("app: run swarm: reserve budget for %s: %w", packetID, err)
	}
	return &SwarmReservation{PacketID: packetID, ChildID: childID, Granted: budgetPerChild}, nil
}

// attributeSwarmChild persists the packet attribution as a steering
// message from the parent, whose sender id sits in the child's
// delegation chain and so passes the delegation-scope gate.
func attributeSwarmChild(ctx context.Context, st *Store, parentID, childID, packetID string) error {
	if _, err := SendSteeringMessage(ctx, st, childID, parentID, "packet "+packetID+" admitted"); err != nil {
		return fmt.Errorf("app: run swarm: attribute %s: %w", packetID, err)
	}
	return nil
}

// swarmRunDurable drives the admission-and-gate run with durable
// receipts pinned to the executed manifest's digest, landing the run
// record, its packet rows and the children it created in the store.
func swarmRunDurable(ctx context.Context, st *Store, m swarm.Manifest, created SwarmExecResult,
	admitter swarm.RunAdmitter, gates *swarm.GateRunner) (swarm.RunResult, error) {
	sha, err := swarmManifestSHA(m)
	if err != nil {
		return swarm.RunResult{}, err
	}
	run, rerr := swarm.Run(ctx, m, admitter, gates, nil, swarm.NewDurableReceipts(st, sha).WithChildren(created.storedChildren()))
	if rerr != nil {
		return run, mapSwarmRunError(rerr)
	}
	return run, nil
}

// ErrSwarmNotAccepted marks a swarm run that completed but was not
// accepted: a gate failed or a packet was rejected. It is the caller's
// own input failing its checks, so ErrorCode maps it onto
// receipt_invalid (exit 8, HTTP 422), never onto internal. A cancelled
// run is also not accepted but keeps ErrSessionCancelled's class.
var ErrSwarmNotAccepted = errors.New("not accepted")

// swarmNotAccepted is the one place a completed-but-rejected run
// becomes an app error; RunSwarm and SwarmRunManifest both use it. The
// message names the failed gates and the packets that were not
// admitted.
func swarmNotAccepted(m swarm.Manifest, run swarm.RunResult) error {
	admitted := make(map[string]bool, len(run.Admitted))
	for _, id := range run.Admitted {
		admitted[id] = true
	}
	rejected := []string{}
	for _, p := range m.Packets {
		if !admitted[p.ID] {
			rejected = append(rejected, p.ID)
		}
	}
	sort.Strings(rejected)
	return fmt.Errorf("app: run swarm %s: %w: failed gates %v, rejected packets %v, admitted %d of %d packets",
		m.Name, ErrSwarmNotAccepted, run.Gates.FailedGateNames(), rejected, len(run.Admitted), len(m.Packets))
}

// mapSwarmRunError is the one place a swarm run failure becomes an app
// error: a cancelled run maps onto ErrSessionCancelled (the CLI's
// exit-130 interrupt contract) with the cause kept; any other failure
// is wrapped with the run-swarm prefix. Both RunSwarm and
// SwarmRunManifest use it.
//
// Only context.Canceled maps: a deadline is a limit the caller imposed, not
// a user interrupt (exit 130 and reason cancelled would mislead), so
// DeadlineExceeded keeps the generic wrapping. CreateChildSession applies
// the same rule (childContextError).
func mapSwarmRunError(err error) error {
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("app: run swarm: %w: %w", ErrSessionCancelled, err)
	}
	return fmt.Errorf("app: run swarm: %w", err)
}

// swarmManifestSHA pins the executed manifest content as the SHA-256
// of its canonical JSON encoding; Go marshals struct fields in
// declaration order, so equal manifests digest identically.
func swarmManifestSHA(m swarm.Manifest) (string, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("app: run swarm: digest manifest: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// compile-time contract: the concrete store satisfies the durable
// receipts sink (RunSwarm persists through it without an adapter).
var _ swarm.RunSink = (*store.Store)(nil)

// ValidateSwarmManifest reads and validates one swarm manifest file
// without executing it. Unknown files map to ErrUsage at the CLI.
func ValidateSwarmManifest(path string) (swarm.Manifest, error) {
	return swarm.LoadManifest(path)
}

// RunSwarmFile composes manifest loading with RunSwarm: an explicit
// budgetTotal anchors the parent's pool (the same semantics as a
// session run's --budget), each admitted packet becomes a delegated
// child with an even budget share, and the durable gate receipts
// persist. Validation failures map to ErrUsage.
func RunSwarmFile(ctx context.Context, st *Store, parent *Session, manifestPath string, budgetTotal int64) (SwarmExecResult, error) {
	m, err := swarm.LoadManifest(manifestPath)
	if err != nil {
		return SwarmExecResult{}, fmt.Errorf("%w: %v", ErrUsage, err)
	}
	if budgetTotal > 0 {
		if err := st.SetBudgetPool(ctx, parent.ID, budgetTotal); err != nil {
			return SwarmExecResult{}, fmt.Errorf("app: run swarm: %w", err)
		}
	}
	share := budgetTotal
	if n := len(m.Packets); n > 0 {
		share = budgetTotal / int64(n)
	}
	return RunSwarm(ctx, st, parent, m, share, AlwaysAdmit{}, swarm.NewGateRunner())
}

// Admit always accepts the packet; gates are the meaningful
// completion check.
func (AlwaysAdmit) Admit(swarm.Packet, []byte) error { return nil }

// storedChildren maps the created children onto the run record's
// child rows, in creation order.
func (r SwarmExecResult) storedChildren() []store.SwarmRunChild {
	out := make([]store.SwarmRunChild, 0, len(r.Children))
	for _, c := range r.Children {
		out = append(out, store.SwarmRunChild{PacketID: c.PacketID, SessionID: c.Session.ID})
	}
	return out
}

// recordCancelledCreation lands the failed run record, naming the
// children already created, when a cancellation cut child creation
// short: the run loop never ran, so nothing else would record them. It
// returns only a recording failure; a run without children records
// nothing, as before.
func recordCancelledCreation(ctx context.Context, st *Store, m swarm.Manifest, res SwarmExecResult,
	admitter swarm.RunAdmitter, gates *swarm.GateRunner) error {
	if len(res.Children) == 0 {
		return nil
	}
	_, err := swarmRunDurable(ctx, st, m, res, admitter, gates)
	if err == nil || errors.Is(err, ErrSessionCancelled) {
		return nil
	}
	return err
}

// settleCancelledSwarm records each unfinished child of a cancelled
// run as cancelled with the parent's reason. A run that was not
// cancelled returns err untouched; a recording failure joins err so
// the interrupt class is kept. The reason is read before the context
// is detached, as WithoutCancel drops the cancellation cause.
func settleCancelledSwarm(ctx context.Context, st *Store, res SwarmExecResult, err error) error {
	if !errors.Is(err, ErrSessionCancelled) || len(res.Children) == 0 {
		return err
	}
	reason := CancelReason(ctx)
	rec := cancelSwarmChildren(context.WithoutCancel(ctx), st, res.Children, reason)
	return errors.Join(err, rec)
}

// cancelSwarmChildren records every child that has not reached a
// terminal status as cancelled: the session.cancelled event first, then
// the status, as on every other terminal path. A child that already
// completed, failed or was cancelled keeps its status (its reservation is
// still settled). ctx must be
// detached from the cancellation, as EventWriter.Append refuses a done
// context; the single writer per session is closed before returning.
func cancelSwarmChildren(ctx context.Context, st *Store, children []SwarmChild, reason string) error {
	var errs []error
	for _, c := range children {
		if err := cancelSwarmChild(ctx, st, c.Session.ID, reason); err != nil {
			errs = append(errs, fmt.Errorf("app: run swarm: cancel child %s: %w", c.PacketID, err))
		}
	}
	return errors.Join(errs...)
}

// cancelSwarmChild cancels one child unless it is already terminal. A
// terminal child still has its reservation settled: an earlier settle may
// have failed, and settling a settled reservation is a no-op (only a
// reserved one is updated), so the retry is always safe.
func cancelSwarmChild(ctx context.Context, st *Store, id, reason string) error {
	cur, err := st.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if delegatedParentTerminal[cur.Status] {
		return settleChildReservation(ctx, st, id)
	}
	w, err := st.NewEventWriter(ctx, id)
	if err != nil {
		return err
	}
	defer w.Close()
	if _, err := w.Append(ctx, session.ActorEngine, session.TypeSessionCancelled,
		map[string]any{"status": "cancelled", "reason": reason}); err != nil {
		return err
	}
	if err := st.UpdateSessionStatus(ctx, id, "cancelled"); err != nil {
		return err
	}
	return settleChildReservation(ctx, st, id)
}

// settleChildReservation marks the child's reservation charged, tagging a
// failure with ErrReservationSettle; a child without one is untouched.
func settleChildReservation(ctx context.Context, st *Store, id string) error {
	if err := st.SetReservationCharged(ctx, id); err != nil {
		return fmt.Errorf("%w: %w", ErrReservationSettle, err)
	}
	return nil
}

// ReasonSwarmAborted is the cancellation reason recorded on children
// whose swarm run stopped for a reason other than cancellation.
const ReasonSwarmAborted = "swarm_aborted"

// abortSwarmCreation closes out a run whose child creation failed for a
// reason other than cancellation: the children already created are
// cancelled with ReasonSwarmAborted, their reservations settled, and a
// failed run naming them is stored, as for a cancelled run. It returns
// err, the original failure, joined with any recording failure so its
// class is kept.
func abortSwarmCreation(ctx context.Context, st *Store, m swarm.Manifest, res SwarmExecResult,
	admitter swarm.RunAdmitter, gates *swarm.GateRunner, err error) error {
	if len(res.Children) == 0 {
		return err
	}
	// A cancelled copy makes the run record without evaluating any gate.
	actx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	cancel(&CancelCause{Reason: ReasonSwarmAborted})
	rec := recordCancelledCreation(actx, st, m, res, admitter, gates)
	can := cancelSwarmChildren(context.WithoutCancel(ctx), st, res.Children, ReasonSwarmAborted)
	return errors.Join(err, rec, can)
}
