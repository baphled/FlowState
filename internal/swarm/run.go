package swarm

import (
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
func Run(m Manifest, admitter RunAdmitter, gates *GateRunner, ev RunEvents, receipts RunReceipts) (RunResult, error) {
	if err := ValidateManifest(m); err != nil {
		return RunResult{}, fmt.Errorf("swarm run: %w", err)
	}
	if admitter == nil || gates == nil {
		return RunResult{}, fmt.Errorf("swarm run: admitter and gates are required")
	}
	res := RunResult{Manifest: m}
	allAdmitted := true
	for _, p := range RunOrder(m) {
		err := admitter.Admit(p, sourceBody(p))
		if err != nil {
			allAdmitted = false
			if err := persistRunAdmission(receipts, m.Name, p, false, err.Error()); err != nil {
				return res, err
			}
			if ev != nil {
				ev.PacketRejected(p.ID, err.Error())
			}
			continue
		}
		if err := persistRunAdmission(receipts, m.Name, p, true, ""); err != nil {
			return res, err
		}
		res.Admitted = append(res.Admitted, p.ID)
		if ev != nil {
			ev.PacketAdmitted(p)
		}
	}
	res.Gates = gates.Evaluate(m)
	res.Gates.Accepted = res.Gates.Accepted && allAdmitted && len(res.Admitted) == len(m.Packets)
	if receipts != nil {
		if err := receipts.RecordGates(m.Name, res.Gates); err != nil {
			res.Gates.Accepted = false
			return res, fmt.Errorf("swarm run: persist gates: %w", err)
		}
	}
	return res, nil
}

// persistRunAdmission is the common fail-closed receipt boundary for
// accepted and rejected packets; it is a no-op when no sink is configured.
func persistRunAdmission(receipts RunReceipts, runID string, p Packet, accepted bool, reason string) error {
	if receipts == nil {
		return nil
	}
	if err := receipts.RecordAdmission(runID, p, accepted, reason); err != nil {
		return fmt.Errorf("swarm run: persist admission %s: %w", p.ID, err)
	}
	return nil
}
