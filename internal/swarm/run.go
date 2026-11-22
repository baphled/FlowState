package swarm

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// RunOrder returns the manifest's packets in a stable order where
// prerequisites come first (Kahn's algorithm on the prerequisite
// edges); ambiguity resolves by manifest order.
func RunOrder(m Manifest) []Packet {
	byID := map[string]Packet{}
	deps := map[string]int{}
	children := map[string][]string{}
	for _, p := range m.Packets {
		byID[p.ID] = p
		deps[p.ID] = len(p.Prerequisites)
		for _, pre := range p.Prerequisites {
			children[pre] = append(children[pre], p.ID)
		}
	}
	var order []Packet
	var ready []string
	for _, p := range m.Packets { // manifest order seeds the ready set
		if deps[p.ID] == 0 {
			ready = append(ready, p.ID)
		}
	}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, byID[id])
		for _, c := range children[id] {
			deps[c]--
			if deps[c] == 0 {
				ready = append(ready, c)
			}
		}
	}
	return order
}

// sourceBody fetches the declared source body of one packet: the
// local file whose path equals the packet Task when Task names an
// existing file, else the task text itself. Operators pin real
// bodies by pointing Task at a source file.
func sourceBody(p Packet) []byte {
	if !strings.Contains(p.Task, "\n") {
		if raw, err := os.ReadFile(p.Task); err == nil {
			return raw
		}
	}
	return []byte(p.Task)
}

// Run drives a configured swarm. A passing gate never overrides a
// rejected packet or an inability to persist mandatory evidence.
// Admission decisions are recorded before reporting them as admitted.
//
// Cancelling ctx stops the run between packets and between gates, and
// stops a running gate command. Recording is deliberately detached from
// that cancellation (context.WithoutCancel): an admission decision or a
// gate result that already happened is persisted whole, and the run
// always ends with its gate decision recorded, so a cancelled run is
// stored as a not-accepted (fail) run instead of a torn one. Run then
// returns an error wrapping ctx.Err() when the cancellation cut the run
// short (a packet or gate skipped, or a gate interrupted); a run whose
// gates all finished before a late cancellation keeps its real result.
func Run(ctx context.Context, m Manifest, admitter RunAdmitter, gates *GateRunner,
	ev RunEvents, receipts RunReceipts) (RunResult, error) {
	if err := ValidateManifest(m); err != nil {
		return RunResult{}, fmt.Errorf("swarm run: %w", err)
	}
	if admitter == nil || gates == nil {
		return RunResult{}, fmt.Errorf("swarm run: admitter and gates are required")
	}
	res := RunResult{Manifest: m}
	// Receipts and events outlive cancellation: see the Run comment.
	record := context.WithoutCancel(ctx)
	allAdmitted, stopped := true, false
	for _, p := range RunOrder(m) {
		if ctx.Err() != nil {
			allAdmitted, stopped = false, true
			break // safe to abandon: nothing is half-recorded for p
		}
		admitted, err := admitPacket(record, admitter, ev, receipts, m.Name, p)
		if err != nil {
			return res, err
		}
		if !admitted {
			allAdmitted = false
			continue
		}
		res.Admitted = append(res.Admitted, p.ID)
	}
	res.Gates = gates.Evaluate(ctx, m)
	res.Gates.Accepted = res.Gates.Accepted && allAdmitted && len(res.Admitted) == len(m.Packets)
	if receipts != nil {
		if err := receipts.RecordGates(record, m.Name, res.Gates); err != nil {
			res.Gates.Accepted = false
			return res, fmt.Errorf("swarm run: persist gates: %w", err)
		}
	}
	if cerr := ctx.Err(); cerr != nil && (stopped || res.Gates.Interrupted) {
		return res, fmt.Errorf("swarm run: cancelled: %w", cerr)
	}
	return res, nil
}

// admitPacket admits one packet, records the decision and reports it. The
// decision is recorded before it is reported; an error means the mandatory
// receipt could not be persisted.
func admitPacket(record context.Context, admitter RunAdmitter, ev RunEvents, receipts RunReceipts,
	runID string, p Packet) (bool, error) {
	if err := admitter.Admit(p, sourceBody(p)); err != nil {
		if perr := persistRunAdmission(record, receipts, runID, p, false, err.Error()); perr != nil {
			return false, perr
		}
		if ev != nil {
			ev.PacketRejected(record, p.ID, err.Error())
		}
		return false, nil
	}
	if err := persistRunAdmission(record, receipts, runID, p, true, ""); err != nil {
		return false, err
	}
	if ev != nil {
		ev.PacketAdmitted(record, p)
	}
	return true, nil
}

// persistRunAdmission is the common fail-closed receipt boundary for
// accepted and rejected packets; it is a no-op when no sink is configured.
func persistRunAdmission(ctx context.Context, receipts RunReceipts, runID string, p Packet, accepted bool, reason string) error {
	if receipts == nil {
		return nil
	}
	if err := receipts.RecordAdmission(ctx, runID, p, accepted, reason); err != nil {
		return fmt.Errorf("swarm run: persist admission %s: %w", p.ID, err)
	}
	return nil
}
