package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

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
// failure aborts the run with the partial result — children already
// created stay active (compensated rollback is M2+). Budget failures
// surface as ErrUsage through the ReserveChildBudget seam. When a
// gate fails or admission rejects, the complete child set stays and
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
		return res, err
	}
	run, err := swarmRunDurable(st, m, admitter, gates)
	res.Admitted, res.Gates = run.Admitted, run.Gates
	if err != nil {
		return res, err
	}
	if !run.Gates.Accepted {
		return res, fmt.Errorf("app: run swarm %s not accepted: failed gates %v, admitted %d of %d packets",
			m.Name, run.Gates.FailedGateNames(), len(run.Admitted), len(m.Packets))
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
// record and its packet rows in the store.
func swarmRunDurable(st *Store, m swarm.Manifest,
	admitter swarm.RunAdmitter, gates *swarm.GateRunner) (swarm.RunResult, error) {
	sha, err := swarmManifestSHA(m)
	if err != nil {
		return swarm.RunResult{}, err
	}
	run, rerr := swarm.Run(m, admitter, gates, nil, swarm.NewDurableReceipts(st, sha))
	if rerr != nil {
		return run, fmt.Errorf("app: run swarm: %w", rerr)
	}
	return run, nil
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
